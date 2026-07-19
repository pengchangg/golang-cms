package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const lockName = "internal_cms_schema_migrations"

var ErrPending = errors.New("数据库存在待执行迁移")

var validMigrationName = regexp.MustCompile(`^[0-9]{6}_[a-z0-9]+(?:_[a-z0-9]+)*\.up\.sql$`)

type Migration struct {
	Version  uint64
	Name     string
	SQL      string
	Checksum string
}

type Status struct {
	Current uint64
	Latest  uint64
	Pending int
}

func Load(source fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return nil, fmt.Errorf("读取迁移目录: %w", err)
	}
	var migrations []Migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		if !validMigrationName.MatchString(entry.Name()) {
			return nil, fmt.Errorf("迁移文件名不合法: %s", entry.Name())
		}
		parts := strings.SplitN(entry.Name(), "_", 2)
		version, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil || version == 0 {
			return nil, fmt.Errorf("迁移版本不合法: %s", entry.Name())
		}
		contents, err := fs.ReadFile(source, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("读取迁移 %s: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(contents)
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     entry.Name(),
			SQL:      string(contents),
			Checksum: hex.EncodeToString(digest[:]),
		})
	}
	slices.SortFunc(migrations, func(a, b Migration) int { return int(a.Version) - int(b.Version) })
	for index := 1; index < len(migrations); index++ {
		if migrations[index-1].Version == migrations[index].Version {
			return nil, fmt.Errorf("迁移版本重复: %06d", migrations[index].Version)
		}
	}
	return migrations, nil
}

func Up(ctx context.Context, db *sql.DB, migrations []Migration) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("获取迁移连接: %w", err)
	}
	defer conn.Close()

	if err := acquireLock(ctx, conn); err != nil {
		return err
	}
	defer releaseLock(conn)

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT UNSIGNED NOT NULL PRIMARY KEY,
		name VARCHAR(255) NOT NULL,
		checksum CHAR(64) NOT NULL,
		dirty BOOLEAN NOT NULL DEFAULT TRUE,
		applied_at DATETIME(6) NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`); err != nil {
		return fmt.Errorf("创建迁移状态表: %w", err)
	}

	applied, err := readApplied(ctx, conn)
	if err != nil {
		return err
	}
	if err := validateApplied(applied, migrations); err != nil {
		return err
	}
	for _, migration := range migrations {
		if row, ok := applied[migration.Version]; ok {
			if row.dirty {
				return fmt.Errorf("迁移 %06d 处于 dirty 状态", migration.Version)
			}
			if row.name != migration.Name || row.checksum != migration.Checksum {
				return fmt.Errorf("迁移 %06d 校验和不匹配", migration.Version)
			}
			continue
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO schema_migrations (version, name, checksum, dirty) VALUES (?, ?, ?, TRUE)", migration.Version, migration.Name, migration.Checksum); err != nil {
			return fmt.Errorf("标记迁移 %06d: %w", migration.Version, err)
		}
		if _, err := conn.ExecContext(ctx, migration.SQL); err != nil {
			return fmt.Errorf("执行迁移 %06d: %w", migration.Version, err)
		}
		if _, err := conn.ExecContext(ctx, "UPDATE schema_migrations SET dirty = FALSE, applied_at = ? WHERE version = ?", time.Now().UTC(), migration.Version); err != nil {
			return fmt.Errorf("完成迁移 %06d: %w", migration.Version, err)
		}
	}
	return nil
}

func Check(ctx context.Context, db *sql.DB, migrations []Migration) (Status, error) {
	status := Status{}
	if len(migrations) > 0 {
		status.Latest = migrations[len(migrations)-1].Version
	}
	var exists int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'schema_migrations'`).Scan(&exists)
	if err != nil {
		return status, fmt.Errorf("检查迁移状态表: %w", err)
	}
	if exists == 0 {
		status.Pending = len(migrations)
		return status, ErrPending
	}
	applied, err := readApplied(ctx, db)
	if err != nil {
		return status, err
	}
	if err := validateApplied(applied, migrations); err != nil {
		return status, err
	}
	for version := range applied {
		status.Current = max(status.Current, version)
	}
	status.Pending = len(migrations) - len(applied)
	if status.Pending > 0 {
		return status, ErrPending
	}
	return status, nil
}

type appliedMigration struct {
	name     string
	checksum string
	dirty    bool
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func validateApplied(applied map[uint64]appliedMigration, migrations []Migration) error {
	if len(applied) > len(migrations) {
		return errors.New("数据库迁移数量超出当前二进制")
	}
	for index, migration := range migrations {
		row, ok := applied[migration.Version]
		if index >= len(applied) {
			if ok {
				return fmt.Errorf("数据库迁移历史不连续: %06d", migration.Version)
			}
			continue
		}
		if !ok {
			return fmt.Errorf("数据库迁移历史缺少前置版本: %06d", migration.Version)
		}
		version := migration.Version
		if row.dirty {
			return fmt.Errorf("迁移 %06d 处于 dirty 状态", version)
		}
		if row.name != migration.Name || row.checksum != migration.Checksum {
			return fmt.Errorf("迁移 %06d 校验和不匹配", version)
		}
	}
	for version := range applied {
		if !slices.ContainsFunc(migrations, func(migration Migration) bool { return migration.Version == version }) {
			return fmt.Errorf("数据库迁移版本 %06d 超出当前二进制", version)
		}
	}
	return nil
}

func readApplied(ctx context.Context, db queryer) (map[uint64]appliedMigration, error) {
	rows, err := db.QueryContext(ctx, "SELECT version, name, checksum, dirty FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("读取迁移状态: %w", err)
	}
	defer rows.Close()
	result := make(map[uint64]appliedMigration)
	for rows.Next() {
		var version uint64
		var migration appliedMigration
		if err := rows.Scan(&version, &migration.name, &migration.checksum, &migration.dirty); err != nil {
			return nil, fmt.Errorf("解析迁移状态: %w", err)
		}
		result[version] = migration
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历迁移状态: %w", err)
	}
	return result, nil
}

func acquireLock(ctx context.Context, conn *sql.Conn) error {
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 30)", lockName).Scan(&acquired); err != nil {
		return fmt.Errorf("获取迁移锁: %w", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return errors.New("获取迁移锁超时")
	}
	return nil
}

func releaseLock(conn *sql.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = conn.ExecContext(ctx, "SELECT RELEASE_LOCK(?)", lockName)
}
