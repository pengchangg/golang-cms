# 仓库指南

## 结构与入口

- `cmd/cms/main.go` 是唯一后端组合根，提供 `serve`、`migrate`、`version` 和 `admin reset-password`；管理 API 位于 `/api/admin/v1`，客户端内容 API 位于 `/api/content/v1`，其余 GET/HEAD 路径由同一进程回退到 React SPA。
- `internal/{auth,identity,permission,schema,content,client,asset,transfer}` 是业务模块；跨模块适配集中在 `internal/integration`，通用基础设施在 `internal/platform`。新增业务路由时要在模块、组合根及 OpenAPI 中同步。
- `web/` 有自己的 `go.mod`，用于阻止根模块的 `go test ./...` 遍历 Node 工程；前端命令必须在 `web/` 运行。
- 前端只通过 `web/src/api/client.ts` 调用同源 `/api/admin/v1`，非安全方法由该客户端自动附加会话中的 `X-CSRF-Token`，不要在页面中另写请求逻辑。

## 验证命令

- 本地快速启动使用 `make dev`：它通过 Podman 在 `127.0.0.1:13306` 创建持久化 MySQL 8.0，关闭 OIDC/OSS，构建前端、迁移、重置本地管理员后在 `http://localhost:18080` 启动；数据库 `DEV_DB_*` 变量只在首次创建容器时生效，修改前须运行 `make dev-clean`。`make dev-clean` 会删除本地数据库卷。
- 后端全量门禁：`go test -count=1 -timeout 300s ./...`，`go test -race -count=1 -timeout 360s ./...`，`go vet ./...`。
- 聚焦 Go 包或测试：`go test ./internal/content`；`go test ./internal/content -run '^TestName$'`。
- 前端首次安装使用 `cd web && npm ci`；验证顺序为 `npm run lint`、`npm run typecheck`、`npm run test`、`npm run build`。`npm run lint` 同时 lint TypeScript 和 `api/openapi/{admin,content}.yaml`。
- 聚焦前端测试：在 `web/` 执行 `npm run test -- src/components/DynamicContentForm.test.tsx`；Vitest 使用 jsdom 和 `src/test/setup.ts`。
- `go test -short ./...` 会跳过 CSV 恰好 100,000 行的容量边界测试；发布前不要用它代替全量测试。
- 素材兼容模式还需验证 `cd web && VITE_ASSETS_ENABLED=false npm run build`。

## 契约与迁移

- `api/openapi/admin.yaml` 和 `api/openapi/content.yaml` 是实际聚合契约；功能变更必须同步运行时路由、对应 `api/openapi/fragments/**`、根文档、Go DTO 和 `web/src/api/types.ts`。不要聚合带 `x-integration-status: pending-implementation` 的操作。
- 迁移通过 `db/migrations/embed.go` 编入二进制，文件名必须为连续的新编号 `NNNNNN_name.up.sql`。已合入或可能执行过的迁移受校验和保护，不得修改、删除、重排或复用编号；修正只能新增向前兼容迁移。
- 每个迁移文件只能包含一个可独立执行的 DDL 或 DML 语句。`serve` 不会自动迁移，数据库存在待执行、dirty、未知或校验和不匹配的迁移时会拒绝启动；先显式运行 `cms migrate`。
- 迁移行为必须用 MySQL 8.0 空库验证，并再次执行 `cms migrate` 验证幂等；普通 `go test ./...` 不连接真实 MySQL，不能替代该检查。

## 运行约束

- `serve` 需要已构建的 `web/dist/index.html`、已完成迁移的 MySQL，以及至少一个启用的应急管理员；本地启动前先构建前端并运行 README 中的 `migrate`、`admin reset-password` 命令。
- OIDC 默认启用；不接企业 SSO 的本地环境需显式 `APP_OIDC_ENABLED=false`，应急网页登录还需 `APP_LOCAL_LOGIN_ENABLED=true`。完整变量和值域以 `.env.example` 和 `internal/platform/config/config.go` 为准，程序不会自动加载 `.env`。
- 应用不终止 TLS；`APP_BASE_URL` 必须是浏览器实际访问的 origin，并同时决定 Origin 校验和 Cookie `Secure` 属性。只有 localhost 或回环 IP 可使用 HTTP；生产由网关终止 TLS 时仍填写外部 `https://` URL。
- 素材功能默认启用，并在监听端口前检查完整 OSS 配置、私有 Bucket ACL、Region 和对象读写。没有真实 OSS 时必须同时设置运行时 `APP_ASSETS_ENABLED=false` 和前端构建时 `VITE_ASSETS_ENABLED=false`；两份聚合 OpenAPI 仍保持完整 V1，不按开关裁剪。
- 远程 MySQL TCP 默认拒绝明文连接；使用 DSN TLS。`MYSQL_ALLOW_INSECURE=true` 仅用于明确隔离的开发网络。
- `VITE_ENABLE_AUTH_MOCK`、`VITE_ENABLE_P1_MOCK`、`VITE_ENABLE_F2_MOCK`、`VITE_ENABLE_F3_MOCK` 只在 Vite DEV 模式且值精确为 `true` 时启用；生产代码不得伪造 API 成功响应。
