package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"cms/db/migrations"
	"cms/internal/app"
	"cms/internal/audit"
	"cms/internal/auth"
	"cms/internal/client"
	"cms/internal/content"
	"cms/internal/identity"
	"cms/internal/permission"
	"cms/internal/platform/apperror"
	"cms/internal/platform/config"
	"cms/internal/platform/database"
	"cms/internal/platform/migrate"
	"cms/internal/schema"
	"cms/internal/version"
	"golang.org/x/term"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	code, err := execute(os.Args[1:], logger)
	if err != nil {
		logger.Error("命令执行失败", "error", err)
	}
	os.Exit(code)
}

func execute(args []string, logger *slog.Logger) (int, error) {
	if len(args) == 0 {
		return 2, errors.New("用法: cms <serve|migrate|version|admin reset-password>")
	}
	command := args[0]
	if command == "version" && len(args) == 1 {
		fmt.Println(version.String())
		return 0, nil
	}
	if command == "admin" {
		if len(args) != 4 || args[1] != "reset-password" {
			return 2, errors.New("用法: cms admin reset-password <username> <display-name>")
		}
		if err := runAdmin(args[2], args[3], logger); err != nil {
			return 1, err
		}
		return 0, nil
	}
	if len(args) != 1 || command != "serve" && command != "migrate" {
		return 2, fmt.Errorf("未知命令；用法: cms <serve|migrate|version|admin reset-password>")
	}
	err := run(command, logger)
	if err != nil {
		return 1, err
	}
	return 0, nil
}

func run(command string, logger *slog.Logger) error {
	cfg, err := config.Load(command)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := database.Open(ctx, cfg.MySQLDSN, false, cfg.AllowInsecureMySQL)
	if err != nil {
		return err
	}
	defer db.Close()
	allMigrations, err := migrate.Load(migrations.Files)
	if err != nil {
		return err
	}
	if command == "migrate" {
		return migrate.Up(ctx, db, allMigrations)
	}
	if _, err := migrate.Check(ctx, db, allMigrations); err != nil {
		return fmt.Errorf("数据库迁移状态不允许启动: %w", err)
	}
	return serve(ctx, logger, cfg, db)
}

func serve(ctx context.Context, logger *slog.Logger, cfg config.Config, db *sql.DB) error {
	var emergencyAdminExists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN local_credentials lc ON lc.user_id=u.id
		WHERE u.enabled=TRUE AND lc.emergency_admin=TRUE)`).Scan(&emergencyAdminExists); err != nil {
		return fmt.Errorf("检查应急管理员: %w", err)
	}
	if !emergencyAdminExists {
		return errors.New("没有启用的应急管理员；请先运行 cms admin reset-password <username> <display-name>")
	}
	root, err := os.OpenRoot(cfg.WebDistDir)
	if err != nil {
		return fmt.Errorf("打开前端资源目录: %w", err)
	}
	defer root.Close()
	web, err := fs.Sub(root.FS(), ".")
	if err != nil {
		return fmt.Errorf("创建前端资源文件系统: %w", err)
	}
	if _, err := fs.Stat(web, "index.html"); err != nil {
		return fmt.Errorf("前端资源缺少 index.html: %w", err)
	}
	auditWriter := audit.SQLWriter{}
	schemaRepository := schema.NewRepository()
	modelAdapter := schema.PermissionModelAdapter{DB: db, Repository: schemaRepository}
	permissionProvider := permission.SQLProvider{DB: db, Models: modelAdapter}
	var oidcClient auth.OIDCClient
	if cfg.OIDCEnabled {
		oidcClient, err = auth.NewCoreOSOIDC(ctx, cfg.OIDCIssuerURL, cfg.OIDCClientID, cfg.OIDCClientSecret, cfg.OIDCRedirectURL)
		if err != nil {
			return fmt.Errorf("初始化 OIDC: %w", err)
		}
	}
	authService, err := auth.NewService(auth.NewSQLStore(db, auditWriter), permissionProvider, oidcClient, auth.SystemClock{}, rand.Reader, cfg.SessionSecret)
	if err != nil {
		return err
	}
	authModule, err := auth.NewModule(authService, cfg.BaseURL, cfg.LocalLoginEnabled)
	if err != nil {
		return err
	}
	transactor := database.NewTransactor(db)
	authorizer := permission.PrincipalAuthorizer{}
	userService := identity.NewUserService(identity.UserDependencies{DB: db, Transactor: transactor, Authorizer: authorizer, Audit: auditWriter})
	roleService := permission.NewService(permission.Dependencies{DB: db, Transactor: transactor, Authorizer: authorizer, Audit: auditWriter, Users: userService, Models: modelAdapter})
	contentService := content.NewService(content.Dependencies{
		DB: db, Transactor: transactor, Repository: content.NewRepository(), ModelRepository: schemaRepository, Audit: auditWriter,
	})
	publishedReader := content.NewPublishedContentReader(db, schemaRepository)
	clientService := client.NewService(client.Dependencies{
		DB: db, Transactor: transactor, Repository: client.NewRepository(), Authorizer: authorizer, Audit: auditWriter,
	})
	schemaService := schema.NewService(schema.Dependencies{
		DB: db, Transactor: transactor, Repository: schemaRepository,
		Authorizer: authorizer, Audit: auditWriter, Content: contentService,
	})
	adminMux := http.NewServeMux()
	identity.NewModule(userService, principalFromRequest).RegisterRoutes(adminMux)
	permission.NewModule(roleService, principalFromRequest).RegisterRoutes(adminMux)
	audit.NewModule(audit.NewReader(db), auditPrincipalFromRequest).RegisterRoutes(adminMux)
	schema.NewModule(schemaService, principalFromRequest).RegisterRoutes(adminMux)
	content.NewModule(contentService, principalFromRequest).RegisterRoutes(adminMux)
	client.NewAdminHandler(clientService, principalFromRequest).RegisterRoutes(adminMux)
	contentMux := http.NewServeMux()
	client.NewContentHandler(clientService, publishedReader).RegisterRoutes(contentMux)
	handler := app.New(
		web,
		authModule,
		app.HandlerModule(authModule.Protect(adminMux), "/api/admin/v1/"),
		app.HandlerModule(contentMux, "/api/content/v1/"),
	)
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务启动", "address", cfg.ListenAddr)
		errCh <- server.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func principalFromRequest(r *http.Request) (identity.Principal, error) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		return identity.Principal{}, &apperror.Error{Kind: apperror.KindUnauthenticated, Code: "session_invalid", Message: "管理会话无效"}
	}
	return principal, nil
}

func auditPrincipalFromRequest(r *http.Request) (audit.Principal, error) {
	principal, err := principalFromRequest(r)
	if err != nil {
		return audit.Principal{}, err
	}
	return audit.Principal{SystemPermissions: principal.SystemPermissions}, nil
}

func runAdmin(username, displayName string, logger *slog.Logger) error {
	cfg, err := config.Load("admin")
	if err != nil {
		return err
	}
	password := os.Getenv("CMS_ADMIN_PASSWORD")
	if password == "" {
		if term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprint(os.Stderr, "应急管理员密码: ")
			value, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return errors.New("读取应急管理员密码失败")
			}
			password = string(value)
		} else {
			reader := bufio.NewReader(os.Stdin)
			value, err := reader.ReadString('\n')
			if err != nil {
				return errors.New("请通过标准输入或 CMS_ADMIN_PASSWORD 提供应急管理员密码")
			}
			password = strings.TrimSpace(value)
		}
	}
	if len(password) < 12 {
		return errors.New("应急管理员密码长度不能少于 12 个字符")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := database.Open(ctx, cfg.MySQLDSN, false, cfg.AllowInsecureMySQL)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := migrate.Check(ctx, db, mustMigrations()); err != nil {
		return err
	}
	service, err := auth.NewService(auth.NewSQLStore(db, audit.SQLWriter{}), nil, nil, auth.SystemClock{}, rand.Reader, cfg.SessionSecret)
	if err != nil {
		return err
	}
	meta := auth.RequestMeta{RequestID: "cmd_admin_reset_password", IP: "local", UserAgent: "cms-cli"}
	if err := service.ResetEmergencyAdmin(ctx, "", username, displayName, password, meta); err != nil {
		return err
	}
	logger.Info("应急管理员已创建或重置", "username", username)
	return nil
}

func mustMigrations() []migrate.Migration {
	all, err := migrate.Load(migrations.Files)
	if err != nil {
		panic(err)
	}
	return all
}
