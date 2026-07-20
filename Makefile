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

.PHONY: dev dev-db dev-web dev-migrate dev-reset-admin dev-stop dev-clean

dev: dev-web dev-reset-admin
	@APP_ASSETS_ENABLED=false APP_OIDC_ENABLED=false APP_LOCAL_LOGIN_ENABLED=true \
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
	@test -d web/node_modules || { cd web && npm ci; }
	@cd web && VITE_ASSETS_ENABLED=false npm run build

dev-migrate: dev-db
	@MYSQL_DSN='$(DEV_MYSQL_DSN)' go run ./cmd/cms migrate

dev-reset-admin: dev-migrate
	@CMS_ADMIN_PASSWORD='$(DEV_ADMIN_PASSWORD)' MYSQL_DSN='$(DEV_MYSQL_DSN)' \
		APP_SESSION_SECRET='$(DEV_SESSION_SECRET)' \
		go run ./cmd/cms admin reset-password '$(DEV_ADMIN_USERNAME)' '$(DEV_ADMIN_DISPLAY_NAME)'
	@printf '本地管理员 %s 的密码已重置\n' '$(DEV_ADMIN_USERNAME)'

dev-stop:
	@if $(PODMAN) container exists $(DEV_DB_CONTAINER); then $(PODMAN) stop $(DEV_DB_CONTAINER); fi

dev-clean:
	@if $(PODMAN) container exists $(DEV_DB_CONTAINER); then $(PODMAN) rm -f $(DEV_DB_CONTAINER); fi
	@if $(PODMAN) volume exists $(DEV_DB_VOLUME); then $(PODMAN) volume rm $(DEV_DB_VOLUME); fi
