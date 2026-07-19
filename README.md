# 企业内部 CMS

基于 Go、React 和 MySQL 8.0 的企业内部动态内容管理系统。当前已完成阶段一，支持认证、RBAC、动态模型、草稿 Revision、审计和对应管理页面。

## 文档

- [系统设计](docs/cms-design.md)
- [多 Agent 开发计划](docs/development-plan.md)
- [F0 共享契约](docs/contracts/f0.md)
- [F1 阶段一契约](docs/contracts/f1.md)

## 环境要求

- Go 1.26
- Node.js 24 LTS
- MySQL 8.0
- Docker（镜像构建时需要）

## 国内镜像

项目内 npm 已通过 `web/.npmrc` 固定使用 npmmirror。首次开发前配置 Go 模块镜像：

```bash
go env -w GOPROXY=https://goproxy.cn,direct
```

Dockerfile 默认使用来游戏 Docker Hub 镜像、`goproxy.cn` 和 npmmirror；如需覆盖 Go 或 npm 镜像，可以在构建时传入 `GOPROXY` 或 `NPM_REGISTRY`。

## 命令

```bash
go run ./cmd/cms version
MYSQL_DSN='cms:cms@tcp(127.0.0.1:3306)/cms' go run ./cmd/cms migrate
CMS_ADMIN_PASSWORD='replace-with-a-strong-password' MYSQL_DSN='cms:cms@tcp(127.0.0.1:3306)/cms' APP_SESSION_SECRET='replace-with-at-least-32-random-bytes' go run ./cmd/cms admin reset-password admin '应急管理员'
MYSQL_DSN='cms:cms@tcp(127.0.0.1:3306)/cms' go run ./cmd/cms serve
```

后端验证：

```bash
go test ./...
go test -race ./...
go vet ./...
```

前端验证：

```bash
cd web
npm ci
npm run lint
npm run typecheck
npm run test
npm run build
```

容器验证：

```bash
docker build -t internal-cms:test .
docker run --rm internal-cms:test version
```

运行配置参考 `.env.example`。生产凭证不得提交到仓库或写入前端环境变量。

远程 MySQL TCP 连接必须在 DSN 中配置经过证书验证的 TLS。本机回环地址可以不启用 TLS；`MYSQL_ALLOW_INSECURE=true` 只允许用于明确隔离的开发或测试网络。

OIDC 默认启用，本地应急网页登录默认关闭。仅在本地验收或企业 SSO 故障恢复期间，可以显式设置 `APP_LOCAL_LOGIN_ENABLED=true`；生产环境恢复 SSO 后应立即关闭。设置 `APP_OIDC_ENABLED=false` 时 OIDC 登录入口返回不可用。
