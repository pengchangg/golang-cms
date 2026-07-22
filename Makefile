.DEFAULT_GOAL := dev

PODMAN ?= podman
DEV_DB_CONTAINER ?= cms-dev-mysql
DEV_DB_VOLUME ?= cms-dev-mysql-data
DEV_DB_IMAGE ?= hub-docker-mirrors.laiyouxi.com/library/mysql:8.0.43
DEV_DB_PORT ?= 13306
DEV_DB_ROOT_PASSWORD ?= cms-root-local
DEV_DB_NAME ?= cms
DEV_DB_USER ?= cms
DEV_DB_PASSWORD ?= cms
DEV_ADMIN_USERNAME ?= admin
DEV_ADMIN_DISPLAY_NAME ?= 本地管理员
DEV_ADMIN_PASSWORD ?= cms-admin-local
DEV_SESSION_SECRET ?= local-development-session-secret-32-bytes
DEV_APP_PORT ?= 18080
DEV_BASE_URL ?= http://localhost:$(DEV_APP_PORT)
DEV_MYSQL_DSN = $(DEV_DB_USER):$(DEV_DB_PASSWORD)@tcp(127.0.0.1:$(DEV_DB_PORT))/$(DEV_DB_NAME)
DEV_ASSETS_ENABLED ?= true
DEV_ASSETS_ENV_FILE ?= $(CURDIR)/.env.assets.local
CMS_VERIFY_MYSQL_DSN ?=
IMAGE ?= internal-cms:verify
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || printf dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || printf unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VITE_ASSETS_ENABLED ?= true

.PHONY: dev dev-db dev-web dev-migrate dev-ensure-admin dev-reset-admin dev-stop dev-clean test-schema-tx test-asset-tx verify verify-upgrade verify-migrations verify-image image

dev: dev-web dev-ensure-admin
	@test '$(DEV_ASSETS_ENABLED)' = 'false' || test -r '$(DEV_ASSETS_ENV_FILE)' || { printf '缺少 S3 兼容对象存储本地配置：%s\n请从 .env.assets.local.example 复制并填写，或使用 DEV_ASSETS_ENABLED=false。\n' '$(DEV_ASSETS_ENV_FILE)'; exit 1; }
	@test '$(DEV_ASSETS_ENABLED)' = 'false' || case "$$(stat -c '%a' '$(DEV_ASSETS_ENV_FILE)')" in *00) ;; *) printf 'S3 兼容对象存储本地配置必须禁止 group/other 访问，请执行：chmod 600 %s\n' '$(DEV_ASSETS_ENV_FILE)'; exit 1;; esac
	@set -a; test '$(DEV_ASSETS_ENABLED)' = 'false' || . '$(DEV_ASSETS_ENV_FILE)'; set +a; \
		APP_ASSETS_ENABLED='$(DEV_ASSETS_ENABLED)' APP_ENV=development SMS_PROVIDER=fixed DEV_SMS_FIXED_CODE=123456 APP_LOCAL_LOGIN_ENABLED=true \
		APP_LISTEN_ADDR='127.0.0.1:$(DEV_APP_PORT)' APP_BASE_URL='$(DEV_BASE_URL)' APP_SESSION_SECRET='$(DEV_SESSION_SECRET)' \
		MYSQL_DSN='$(DEV_MYSQL_DSN)' go run ./cmd/cms serve

dev-db:
	@command -v $(PODMAN) >/dev/null || { printf '%s\n' '未找到 Podman'; exit 1; }
	@if $(PODMAN) container exists $(DEV_DB_CONTAINER); then \
		$(PODMAN) start $(DEV_DB_CONTAINER) >/dev/null; \
	else \
		$(PODMAN) volume exists $(DEV_DB_VOLUME) || $(PODMAN) volume create $(DEV_DB_VOLUME) >/dev/null; \
		$(PODMAN) run -d --name $(DEV_DB_CONTAINER) \
			-p 127.0.0.1:$(DEV_DB_PORT):3306 \
			-v $(DEV_DB_VOLUME):/var/lib/mysql \
			-e MYSQL_ROOT_PASSWORD='$(DEV_DB_ROOT_PASSWORD)' \
			-e MYSQL_DATABASE='$(DEV_DB_NAME)' \
			-e MYSQL_USER='$(DEV_DB_USER)' \
			-e MYSQL_PASSWORD='$(DEV_DB_PASSWORD)' \
			$(DEV_DB_IMAGE) >/dev/null; \
	fi
	@printf '%s' '等待 MySQL 就绪'
	@for attempt in $$(seq 1 60); do \
		if $(PODMAN) exec $(DEV_DB_CONTAINER) mysqladmin ping -h 127.0.0.1 -uroot -p'$(DEV_DB_ROOT_PASSWORD)' --silent >/dev/null 2>&1; then \
			printf '%s\n' '，完成'; exit 0; \
		fi; \
		printf '.'; sleep 1; \
	done; \
	printf '%s\n' '，超时'; $(PODMAN) logs $(DEV_DB_CONTAINER); exit 1

dev-web:
	@current="$$(sha256sum web/package-lock.json | cut -d ' ' -f1)"; \
		stamp="$$(test -r web/node_modules/.cms-package-lock.sha256 && tr -d '\n' < web/node_modules/.cms-package-lock.sha256 || true)"; \
		if test ! -d web/node_modules || test "$$current" != "$$stamp"; then \
			cd web && npm ci && printf '%s\n' "$$current" > node_modules/.cms-package-lock.sha256; \
		fi
	@test '$(DEV_ASSETS_ENABLED)' = 'true' || test '$(DEV_ASSETS_ENABLED)' = 'false' || { printf '%s\n' 'DEV_ASSETS_ENABLED 只能是 true 或 false'; exit 1; }
	@cd web && VITE_ASSETS_ENABLED='$(DEV_ASSETS_ENABLED)' VITE_CONTENT_API_EXPLORER_ENABLED=true npm run build

dev-migrate: dev-db
	@MYSQL_DSN='$(DEV_MYSQL_DSN)' go run ./cmd/cms migrate

dev-ensure-admin: dev-migrate
	@CMS_ADMIN_PASSWORD='$(DEV_ADMIN_PASSWORD)' MYSQL_DSN='$(DEV_MYSQL_DSN)' \
		APP_SESSION_SECRET='$(DEV_SESSION_SECRET)' \
		go run ./cmd/cms admin ensure '$(DEV_ADMIN_USERNAME)' '$(DEV_ADMIN_DISPLAY_NAME)'

dev-reset-admin: dev-migrate
	@CMS_ADMIN_PASSWORD='$(DEV_ADMIN_PASSWORD)' MYSQL_DSN='$(DEV_MYSQL_DSN)' \
		APP_SESSION_SECRET='$(DEV_SESSION_SECRET)' \
		go run ./cmd/cms admin reset-password '$(DEV_ADMIN_USERNAME)' '$(DEV_ADMIN_DISPLAY_NAME)'
	@printf '本地管理员 %s 的密码已重置\n' '$(DEV_ADMIN_USERNAME)'

test-schema-tx:
	@test '$(origin CMS_TEST_MYSQL_DSN)' != 'command line' || { printf '%s\n' '禁止在 make 命令行传递 CMS_TEST_MYSQL_DSN；请通过环境变量注入'; exit 1; }
	@test -n '$(CMS_TEST_MYSQL_DSN)' || { printf '%s\n' '缺少 CMS_TEST_MYSQL_DSN（必须指向已完成迁移的专用 MySQL 测试库）'; exit 1; }
	@go test -count=1 -run 'TestHasAnyContentUsesSingleConnectionTransaction|TestLockedReadsIgnoreEarlierRepeatableReadSnapshot' ./internal/content
	@go test -count=1 -timeout 30s -run 'TestArchiveModelInboundRelationUsesCurrentRead|TestRelationWriteObservesConcurrentModelArchive' ./internal/schema

test-asset-tx:
	@test '$(origin CMS_TEST_MYSQL_DSN)' != 'command line' || { printf '%s\n' '禁止在 make 命令行传递 CMS_TEST_MYSQL_DSN；请通过环境变量注入'; exit 1; }
	@test -n '$(CMS_TEST_MYSQL_DSN)' || { printf '%s\n' '缺少 CMS_TEST_MYSQL_DSN（必须指向已完成迁移的专用 MySQL 测试库）'; exit 1; }
	@go test -count=1 -timeout 30s -run 'TestClientAssetDownloadUsesSingleConnectionTransaction|TestClientAssetDownloadSerializesWithRevocation' ./internal/integration

verify:
	@test '$(origin CMS_VERIFY_MYSQL_DSN)' != 'command line' || { printf '%s\n' '禁止在 make 命令行传递 CMS_VERIFY_MYSQL_DSN；请通过环境变量或 CI secret 注入'; exit 1; }
	@test -n '$(CMS_VERIFY_MYSQL_DSN)' || { printf '%s\n' '缺少 CMS_VERIFY_MYSQL_DSN（完整门禁必须使用专用空 MySQL 8.0 数据库）'; exit 1; }
	@$(MAKE) verify-upgrade
	@env -u CMS_VERIFY_MYSQL_DSN go test -count=1 -timeout 300s ./...
	@env -u CMS_VERIFY_MYSQL_DSN go test -race -count=1 -timeout 360s ./...
	@env -u CMS_VERIFY_MYSQL_DSN go vet ./...
	@cd web && env -u CMS_VERIFY_MYSQL_DSN npm run lint
	@cd web && env -u CMS_VERIFY_MYSQL_DSN npm run typecheck
	@cd web && env -u CMS_VERIFY_MYSQL_DSN npm run test
	@cd web && env -u CMS_VERIFY_MYSQL_DSN npm run build
	@cd web && env -u CMS_VERIFY_MYSQL_DSN VITE_ASSETS_ENABLED=false npm run build
	@$(MAKE) verify-migrations
	@CMS_TEST_MYSQL_DSN="$${CMS_VERIFY_MYSQL_DSN}" $(MAKE) test-schema-tx
	@CMS_TEST_MYSQL_DSN="$${CMS_VERIFY_MYSQL_DSN}" $(MAKE) test-asset-tx
	@env -u CMS_VERIFY_MYSQL_DSN $(MAKE) verify-image

verify-migrations:
	@test -n '$(CMS_VERIFY_MYSQL_DSN)' || { printf '%s\n' '缺少 CMS_VERIFY_MYSQL_DSN（必须指向专用空 MySQL 8.0 数据库）'; exit 1; }
	@MYSQL_DSN="$${CMS_VERIFY_MYSQL_DSN}" go run ./cmd/cms migrate
	@MYSQL_DSN="$${CMS_VERIFY_MYSQL_DSN}" go run ./cmd/cms migrate

verify-upgrade:
	@test -n '$(CMS_VERIFY_MYSQL_DSN)' || { printf '%s\n' '缺少 CMS_VERIFY_MYSQL_DSN（必须指向专用空 MySQL 8.0 数据库）'; exit 1; }
	@CMS_MIGRATION_UPGRADE_DSN="$${CMS_VERIFY_MYSQL_DSN}" go test -count=1 -timeout 180s -run '^TestOIDCSessionCleanupUpgradeOnMySQL$$' ./db/migrations

verify-image:
	@commit="$$(git rev-parse HEAD)"; \
		$(MAKE) image IMAGE=internal-cms:verify VERSION=verify COMMIT="$$commit" BUILD_TIME=1970-01-01T00:00:01Z && \
		test "$$($(PODMAN) run --rm internal-cms:verify version)" = "cms verify (commit=$$commit, built=1970-01-01T00:00:01Z)"

image:
	@$(PODMAN) build --build-arg VERSION='$(VERSION)' --build-arg COMMIT='$(COMMIT)' --build-arg BUILD_TIME='$(BUILD_TIME)' --build-arg VITE_ASSETS_ENABLED='$(VITE_ASSETS_ENABLED)' -t '$(IMAGE)' .

dev-stop:
	@if $(PODMAN) container exists $(DEV_DB_CONTAINER); then $(PODMAN) stop $(DEV_DB_CONTAINER); fi

dev-clean:
	@if $(PODMAN) container exists $(DEV_DB_CONTAINER); then $(PODMAN) rm -f $(DEV_DB_CONTAINER); fi
	@if $(PODMAN) volume exists $(DEV_DB_VOLUME); then $(PODMAN) volume rm $(DEV_DB_VOLUME); fi
