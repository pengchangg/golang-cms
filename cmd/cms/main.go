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
	"cms/internal/asset"
	"cms/internal/audit"
	"cms/internal/auth"
	"cms/internal/client"
	"cms/internal/content"
	"cms/internal/identity"
	"cms/internal/integration"
	"cms/internal/permission"
	"cms/internal/platform/apperror"
	"cms/internal/platform/config"
	"cms/internal/platform/database"
	"cms/internal/platform/migrate"
	"cms/internal/platform/task"
	"cms/internal/schema"
	"cms/internal/transfer"
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
		return 2, errors.New("用法: cms <serve|migrate|version|admin ensure|admin reset-password>")
	}
	command := args[0]
	if command == "version" && len(args) == 1 {
		fmt.Println(version.String())
		return 0, nil
	}
	if command == "admin" {
		if len(args) != 4 || args[1] != "ensure" && args[1] != "reset-password" {
			return 2, errors.New("用法: cms admin <ensure|reset-password> <username> <display-name>")
		}
		if err := runAdmin(args[2], args[3], args[1] == "ensure", logger); err != nil {
			return 1, err
		}
		return 0, nil
	}
	if len(args) != 1 || command != "serve" && command != "migrate" {
		return 2, fmt.Errorf("未知命令；用法: cms <serve|migrate|version|admin ensure|admin reset-password>")
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
	authService, err := auth.NewService(auth.NewSQLStore(db, auditWriter), permissionProvider, integration.AuthModelSummaryProvider{DB: db}, oidcClient, auth.SystemClock{}, rand.Reader, cfg.SessionSecret)
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
	var media content.MediaReferenceManager
	var objectStore *asset.OSSStore
	if cfg.AssetsEnabled {
		objectStore, err = asset.NewOSSStore(asset.OSSConfig{Endpoint: cfg.OSSEndpoint, Region: cfg.OSSRegion, Bucket: cfg.OSSBucket, AccessKeyID: cfg.OSSAccessKeyID, AccessKeySecret: cfg.OSSAccessKeySecret, SecurityToken: cfg.OSSSecurityToken, UploadMaxTTL: cfg.OSSUploadTTL, DownloadMaxTTL: cfg.OSSDownloadTTL})
		if err != nil {
			return fmt.Errorf("初始化 OSS: %w", err)
		}
		if err = objectStore.CheckPrivateBucket(ctx); err != nil {
			return fmt.Errorf("检查私有 OSS Bucket: %w", err)
		}
		media = integration.MediaReferenceManager{Manager: asset.SQLReferenceManager{}}
	}
	contentService := content.NewService(content.Dependencies{
		DB: db, Transactor: transactor, Repository: content.NewRepository(), ModelRepository: schemaRepository, Audit: auditWriter,
		Media: media,
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
	var runner *task.Runner
	if cfg.AssetsEnabled {
		assetService, serviceErr := asset.NewService(asset.Dependencies{DB: db, Transactor: transactor, Repository: asset.SQLRepository{}, Store: objectStore, Audit: auditWriter, Config: asset.Config{AllowedMimeTypes: cfg.AssetMimeTypes, MaxSize: cfg.AssetMaxSize, UploadTTL: cfg.OSSUploadTTL, DownloadTTL: cfg.OSSDownloadTTL}})
		if serviceErr != nil {
			return fmt.Errorf("初始化素材服务: %w", serviceErr)
		}
		asset.NewHandler(assetService, principalFromRequest).RegisterRoutes(adminMux)
		integration.ClientAssetHandler{DB: db, Client: clientService, Assets: assetService}.RegisterRoutes(contentMux)

		transferRepository := integration.TransferRepository{SQLRepository: transfer.NewRepository(db)}
		transferStore := integration.TransferStore{Store: objectStore}
		modelReader := integration.ModelReader{DB: db, Repository: schemaRepository}
		transferService := transfer.NewService(transfer.Dependencies{DB: db, Transactor: transactor, Repository: transferRepository, Models: modelReader, Uploads: integration.UploadManager{DB: db, Store: objectStore, MaxSize: cfg.AssetMaxSize}, Store: transferStore, UploadTTL: cfg.OSSUploadTTL, DownloadTTL: cfg.OSSDownloadTTL})
		transfer.NewModule(transferService, integration.TransferPrincipalProvider(principalFromRequest)).RegisterRoutes(adminMux)
		principals := integration.PrincipalSnapshot{DB: db, Permissions: permissionProvider}
		jobHandler := transfer.NewJobHandler(transfer.JobHandler{Repository: transferRepository, Store: transferStore, Importer: contentService, Validator: integration.DraftValidator{Content: contentService, DB: db}, Exporter: integration.ExportSource{Content: contentService, Principals: principals, Models: modelReader}, Principals: principals})
		registry := task.NewRegistry()
		if err = registry.Register(string(transfer.JobCSVImport), jobHandler.TaskHandler()); err != nil {
			return err
		}
		if err = registry.Register(string(transfer.JobCSVExport), jobHandler.TaskHandler()); err != nil {
			return err
		}
		taskStore, storeErr := task.NewSQLStore(db)
		if storeErr != nil {
			return storeErr
		}
		runner, err = task.NewRunner(taskStore, registry, task.RunnerConfig{Owner: cfg.WorkerOwner, Concurrency: cfg.WorkerConcurrency, PollInterval: cfg.WorkerPollInterval, LeaseDuration: cfg.WorkerLeaseDuration, RenewInterval: cfg.WorkerRenewInterval})
		if err != nil {
			return fmt.Errorf("初始化任务 Worker: %w", err)
		}
	}
	handler := app.New(
		web,
		authModule,
		app.HandlerModule(authModule.Protect(csvUploadStatusHandler(adminMux)), "/api/admin/v1/"),
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
	services := []func(context.Context) error{func(context.Context) error {
		logger.Info("HTTP 服务启动", "address", cfg.ListenAddr)
		return server.ListenAndServe()
	}}
	if runner != nil {
		services = append(services, func(workerCtx context.Context) error {
			err := runner.Run(workerCtx)
			if err == nil && workerCtx.Err() == nil {
				return errors.New("任务 Worker 意外停止")
			}
			return err
		})
	}
	return runParallel(ctx, func() error {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownCancel()
		return server.Shutdown(shutdownCtx)
	}, services...)
}

// csvUploadStatusHandler 补齐 transfer 通用 HTTP 层尚未提供的上传超限状态映射。
func csvUploadStatusHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/imports/uploads") {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &bufferedResponse{header: make(http.Header)}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		if status == http.StatusBadRequest && strings.Contains(recorder.body.String(), `"code":"file_too_large"`) {
			status = http.StatusRequestEntityTooLarge
		}
		for key, values := range recorder.header {
			w.Header()[key] = values
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(recorder.body.String()))
	})
}

type bufferedResponse struct {
	header http.Header
	body   strings.Builder
	status int
}

func (w *bufferedResponse) Header() http.Header { return w.header }
func (w *bufferedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *bufferedResponse) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(value)
}

func runParallel(ctx context.Context, shutdown func() error, services ...func(context.Context) error) error {
	return runParallelWithTimeout(ctx, shutdown, 30*time.Second, func(err error) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}, services...)
}

var errParallelShutdownTimeout = errors.New("服务整体关闭超时")

func runParallelWithTimeout(ctx context.Context, shutdown func() error, timeout time.Duration, hardStop func(error), services ...func(context.Context) error) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(services))
	for _, service := range services {
		go func() { results <- service(runCtx) }()
	}
	first := error(nil)
	received := 0
	select {
	case first = <-results:
		received = 1
		cancel()
	case <-ctx.Done():
		cancel()
	}
	shutdownResult := make(chan error, 1)
	go func() { shutdownResult <- shutdown() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var shutdownErr error
	for received < len(services) || shutdownResult != nil {
		select {
		case err := <-results:
			received++
			if first == nil && err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
				first = err
			}
		case shutdownErr = <-shutdownResult:
			shutdownResult = nil
		case <-timer.C:
			hardStop(errParallelShutdownTimeout)
			panic("服务硬终止函数意外返回")
		}
	}
	if first != nil && !errors.Is(first, http.ErrServerClosed) && !errors.Is(first, context.Canceled) {
		return first
	}
	return shutdownErr
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

func runAdmin(username, displayName string, ensure bool, logger *slog.Logger) error {
	cfg, err := config.Load("admin")
	if err != nil {
		return err
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
	if ensure {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM local_credentials lc JOIN users u ON u.id = lc.user_id WHERE lc.username = ? AND lc.emergency_admin = TRUE AND u.enabled = TRUE)`, username).Scan(&exists); err != nil {
			return fmt.Errorf("检查应急管理员: %w", err)
		}
		if exists {
			logger.Info("应急管理员已存在，跳过初始化", "username", username)
			return nil
		}
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
	service, err := auth.NewService(auth.NewSQLStore(db, audit.SQLWriter{}), nil, nil, nil, auth.SystemClock{}, rand.Reader, cfg.SessionSecret)
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
