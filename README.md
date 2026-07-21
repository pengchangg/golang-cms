# 企业内部 CMS

基于 Go、React 和 MySQL 8.0 的企业内部动态内容管理系统。三个交付阶段均已完成，支持认证与权限、动态内容审核发布、S3 兼容对象存储素材、CSV 导入导出和持久化后台任务。

## 文档

- [系统设计](docs/cms-design.md)
- [多 Agent 开发计划](docs/development-plan.md)
- [F0 共享契约](docs/contracts/f0.md)
- [F1 阶段一契约](docs/contracts/f1.md)
- [F2 阶段二契约](docs/contracts/f2.md)
- [F3 阶段三契约](docs/contracts/f3.md)
- [API Key 使用指南](docs/api-key-guide.md)

## 阶段二能力

- 固定的草稿、待审核、驳回、发布和下线工作流
- 审核动作按模型权限授权，审核通过时原子切换发布指针
- Revision 级类型化投影、动态过滤排序和一层关联展开
- API Key 模型范围、过期、撤销和无宽限期轮换
- `/api/content/v1` 只读已发布内容，支持强 ETag 和条件请求
- 审核队列、工作流事件和 API Key 管理页面

## 阶段三能力

- S3 兼容对象存储直传、确认、签名下载和素材状态管理
- 单媒体、多媒体及嵌套媒体的 Revision 级引用
- MySQL 任务租约、多 Worker 防重复、续租、重试和取消
- CSV 模板、流式导入、行级错误报告和带 BOM 导出
- 素材库、媒体选择器、导入导出和任务管理页面

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

Dockerfile 默认使用来游戏 Docker Hub 镜像、`goproxy.cn` 和 npmmirror；如需覆盖 Go 或 npm 镜像，可以在构建时传入 `GOPROXY` 或 `NPM_REGISTRY`。前端素材能力通过构建参数 `VITE_ASSETS_ENABLED` 注入，默认值为 `true`。

## 命令

本地快速运行需要 Podman。以下命令会在 `127.0.0.1:13306` 自动创建持久化 MySQL 8.0 容器、构建前端、执行迁移、初始化本地管理员，并在 `http://localhost:18080` 启动应用；本地模式关闭 OIDC，默认启用 S3 兼容对象存储：

```bash
make dev
```

首次启动前执行 `install -m 600 .env.assets.local.example .env.assets.local`，再填写 Endpoint、Bucket、Region 和 S3 凭证。阿里云 OSS 可使用示例中的官方 S3 兼容 Endpoint；Bucket 已绑定 CNAME 时使用该 CNAME 并设置 `S3_BUCKET_ENDPOINT=true`。该本地文件被 Git 忽略且只由后端启动进程读取，AccessKey 不会传给 Vite。已有 `.env.assets.local` 不会自动更新，升级者必须手工将旧 OSS 变量改为模板中的 `S3_*` 变量，并将 Bucket CORS 的允许 Header 从 `x-oss-meta-sha256` 更新或补充为 `Content-Type`、`x-amz-meta-sha256`；兼容服务支持标准条件写时还需允许 `If-None-Match`。暂时没有对象存储时可执行 `make dev DEV_ASSETS_ENABLED=false`，前后端素材能力会同步关闭。

默认管理员为 `admin / cms-admin-local`。`make dev` 只在该管理员不存在时初始化，不会重置已有密码或撤销已有会话；修改 `DEV_ADMIN_PASSWORD` 后需显式执行 `make dev-reset-admin`，该命令会撤销该管理员的全部现有会话。应用端口可直接覆盖，例如 `make dev DEV_APP_PORT=8080`。数据库端口、名称和凭证只在首次创建容器时生效；修改这些值前先运行 `make dev-clean`，再使用相同的 `DEV_DB_*` 变量执行 `make dev`。`make dev-stop` 停止 MySQL 但保留数据；`make dev-clean` 删除本地 MySQL 容器和数据卷。

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

OIDC 默认启用，本地应急网页登录默认关闭。仅在本地验收或企业 SSO 故障恢复期间，可以显式设置 `APP_LOCAL_LOGIN_ENABLED=true`；生产环境恢复 SSO 后应立即关闭。设置 `APP_OIDC_ENABLED=false` 时 OIDC 登录入口返回不可用。应用自身监听普通 HTTP；`APP_BASE_URL` 应填写浏览器实际访问的 origin，Cookie 的 `Secure` 属性跟随其 scheme。只有 localhost 或回环 IP 可使用 `http://`；生产由网关终止 TLS 时，`APP_BASE_URL` 仍填写外部 `https://` 地址。

素材属于 V1 默认能力。同步 CSV 导入导出不依赖素材或对象存储。`APP_ASSETS_ENABLED` 和前端构建变量 `VITE_ASSETS_ENABLED` 均默认 `true`；生产部署启用素材时，必须完整配置 `.env.example` 中必需的 `S3_*`、签名 TTL、MIME 列表和最大尺寸。`S3_ENDPOINT` 必须是仅含 origin 的 HTTPS URL，可使用自定义主机名和端口；`S3_USE_PATH_STYLE` 默认 `false`，使用要求路径寻址的服务时才启用；Endpoint 已经绑定单个 Bucket 时改用 `S3_BUCKET_ENDPOINT=true`。应用会在注册素材路由前验证配置、Bucket 访问、可用时的 Region/ACL、匿名读写拒绝、认证对象读写和标准条件写能力，检查失败即拒绝启动；兼容服务明确不支持条件写时仅关闭 `If-None-Match` 保护。浏览器直传要求 Bucket CORS 放行 CMS Origin、`PUT` 方法以及 `Content-Type`、`x-amz-meta-sha256` Header，支持条件写时还需允许 `If-None-Match`。AccessKey、SessionToken 和签名 URL不得写入日志。

不使用素材时，可以将运行时 `APP_ASSETS_ENABLED=false`，并在构建前端或镜像时同时设置 `VITE_ASSETS_ENABLED=false`（例如 `docker build --build-arg VITE_ASSETS_ENABLED=false ...`）。此时前端不展示素材和媒体选择入口，后端不注册素材路由；同步 CSV 导入导出仍然可用。`api/openapi/admin.yaml` 与 `api/openapi/content.yaml` 仍是完整 V1 聚合契约，不随部署开关裁剪。
