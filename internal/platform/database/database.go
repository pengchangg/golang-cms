package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

func Open(ctx context.Context, dsn string, multiStatements, allowInsecure bool) (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析 MYSQL_DSN: %w", err)
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.MultiStatements = multiStatements
	if err := requireSecureTransport(cfg, allowInsecure); err != nil {
		return nil, err
	}
	if cfg.Params == nil {
		cfg.Params = make(map[string]string)
	}
	cfg.Params["charset"] = "utf8mb4"
	cfg.Params["time_zone"] = "'+00:00'"

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("打开 MySQL: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接 MySQL: %w", err)
	}
	if err := requireMySQL8(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func requireSecureTransport(cfg *mysql.Config, allowInsecure bool) error {
	if cfg.Net != "tcp" || cfg.TLSConfig != "" || allowInsecure {
		return nil
	}
	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return fmt.Errorf("解析 MySQL 地址: %w", err)
	}
	ip := net.ParseIP(host)
	if host == "localhost" || ip != nil && ip.IsLoopback() {
		return nil
	}
	return errors.New("远程 MySQL TCP 连接必须在 DSN 中配置 TLS；仅开发环境可显式设置 MYSQL_ALLOW_INSECURE=true")
}

func requireMySQL8(ctx context.Context, db *sql.DB) error {
	var value string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&value); err != nil {
		return fmt.Errorf("读取 MySQL 版本: %w", err)
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "mariadb") || !strings.HasPrefix(value, "8.0.") {
		return fmt.Errorf("需要 Oracle MySQL 8.0，当前版本为 %q", value)
	}
	return nil
}
