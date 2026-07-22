# CMS 全量代码审计报告

审计日期：2026-07-21  
审计基线：`e1acab0`（`main`，相对 `origin/main` 超前 1 个提交）  
审计范围：后端、前端、认证授权、内容工作流、素材、CSV、OpenAPI、迁移、容器与工程门禁  
审计深度：Deep

## 1. 结论

项目已经具备清晰的模块边界、集中组合根、参数化 SQL、事务化业务写入、严格迁移校验和较完整的单元测试，基础质量明显高于一般内部管理系统。但当前不建议直接作为高权限生产 CMS 发布，首要阻断项是权限委派边界：拥有单项管理权限的账户可以把自身能力扩展到更高权限、接管其他短信账户，或签发超出自身内容权限的 API Key。

综合评分：**6.2 / 10**

| 维度 | 分数 | 结论 |
|---|---:|---|
| 架构 | 6.4 | 模块和组合根清晰，但事务依赖、Schema 演进及内容服务职责仍有结构性风险 |
| 代码质量 | 6.7 | 可读性和错误模型较好，核心大文件、状态生命周期和边界测试仍需收敛 |
| 工程化 | 6.8 | 本地门禁较完整，缺少统一验证入口、CI 和运行时契约校验 |
| 性能与风险 | 4.8 | 授权委派、连接池互等、资源治理和生产配置约束是主要短板 |

发现统计：

| 严重级别 | 数量 |
|---|---:|
| 高 | 7 |
| 中 | 11 |
| 低/结构性 | 3 |

发布建议：**阻断生产发布，完成 P0 授权修复和对应回归测试后重新审计。**

## 2. 高风险发现

### H-01 `roles.manage` 可以直接完成自提权

- 位置：`internal/permission/service.go:151-184`、`internal/permission/service.go:187-215`、`internal/permission/service.go:217-267`
- 触发条件：操作者拥有 `roles.manage`，但不拥有某个高权限角色或待授予权限。
- 结果：操作者可以给自己绑定任意现存角色，也可以修改自己当前角色的系统权限和模型权限。权限在后续请求中重新加载，因此无需重新登录即可提权。
- 现有保护：只校验 `roles.manage`、角色存在、权限代码合法和模型 active；没有禁止修改自己，也没有“授予权限不得超过操作者有效权限”的约束。
- 修复建议：将角色资料管理与安全授权拆分；禁止修改自己的角色及其生效权限；待授予权限必须是操作者有效权限的子集；高权限授权要求近期重新认证或双人审批。
- 必要测试：自绑定高权限角色、修改自身角色、越范围系统权限、越范围模型权限均返回 403。

### H-02 `users.manage` 可以接管任意短信账户

- 位置：`internal/identity/service.go:127-170`、`internal/identity/repository.go:201-209`、`internal/auth/service.go:134-156`
- 触发条件：操作者拥有 `users.manage` 和 `roles.view`，把目标用户手机号改为自己控制且尚未绑定的手机号。
- 结果：目标会话被撤销后，操作者可通过正常短信验证以目标用户身份登录，并取得目标用户全部权限。
- 现有保护：只校验操作者权限、目标是短信账户和手机号唯一性；没有保护应急管理员、高权限用户或已激活凭证。
- 修复建议：新增独立高危凭证管理权限；普通管理员只能为未激活用户设置初始手机号；替换已激活手机号需目标确认或双人审批；禁止修改权限高于操作者的用户和应急管理员。
- 必要测试：低权限管理员不能修改高权限用户、应急管理员或自身以外已激活账户的手机号。

### H-03 API Key 可被签发到创建者无权访问的模型

- 位置：`internal/client/service.go:92-113`、`internal/client/service.go:138-183`、`internal/client/repository.go:106-131`
- 触发条件：操作者拥有全局 `api_keys.create`，但没有目标模型的 `content.view`，创建或轮换 Key 时提交该模型 ID。
- 结果：服务只检查模型 active，随后将模型写入 Key scope；内容 API 以后只认 Key scope，不再检查创建者权限。
- 现有保护：Key 随机性、哈希存储和一次性返回均有效，但不能限制授权范围。
- 修复建议：创建和轮换时要求每个模型均位于操作者可查看模型集合；如确需跨范围签发，新增语义明确且受严格控制的 `api_keys.create_any_scope`。
- 必要测试：无目标模型 `content.view` 时，创建和轮换均返回 403。

### H-04 Schema 更新可使当前内容立即违反公开 Schema

- 位置：`internal/schema/service.go:375-388`、`internal/schema/service.go:407-438`
- 触发条件：模型已有草稿或发布内容后，将可选字段改为必填，或收紧文本长度、数字范围等约束。
- 结果：新 Schema 会立即公开，但历史草稿和线上 Revision 未重新验证，可能继续返回违反 Schema 的内容；后续编辑也可能无法保存。
- 现有保护：只禁止有内容时修改字段类型、删除枚举值和启用需回填的投影能力，未覆盖普通约束收紧。
- 修复建议：定义 Schema 兼容性规则；有内容时拒绝不兼容收紧，或在同一受控流程中验证全部当前草稿和发布 Revision；大规模变化走显式迁移任务。
- 必要测试：`required`、`max_length`、`minimum/maximum` 分别覆盖草稿和发布版本。

### H-05 事务中重新使用全局连接池，可形成池级互等

- 位置：`internal/schema/service.go:337`、`internal/schema/service.go:417`、`internal/schema/service.go:430`、`internal/content/service.go:75`、`internal/platform/database/database.go:36`
- 触发条件：接近 25 个并发字段更新各自先持有事务连接，再由 `HasAnyContent` 通过全局 `*sql.DB` 申请第二个连接。
- 结果：连接池上限为 25 时，所有事务都可能持有一个连接并等待第二个连接，直到上下文超时，期间数据库池整体不可用。
- 现有保护：事务回调提供了 `database.Querier`，但 `ContentExistenceChecker` 接口没有接收它，因而无法加入当前事务。
- 修复建议：让 `HasAnyContent` 接收当前 `database.Querier` 并传入事务连接；增加 `MaxOpenConns=1/2` 的并发回归测试。

### H-06 客户端素材下载存在同类连接池互等

- 位置：`internal/integration/client_scope.go:57-104`、`internal/asset/service.go:430-436`
- 触发条件：并发素材下载先通过事务锁定 API Key，再在事务提交前调用 `PublishedDownload`，后者从全局连接池申请第二个连接。
- 结果：高并发下可耗尽连接池；即使没有完全互等，每次下载也会放大数据库连接占用。
- 现有保护：`FOR UPDATE` 保证撤销/轮换原子性，但素材查询接口不接收当前事务连接。
- 修复建议：授权相关查询使用同一事务连接；完成需要锁保护的检查后尽早提交，再执行不需要数据库锁的签名步骤。

### H-07 编辑器本地状态可跨条目污染并覆盖其他内容

- 位置：`web/src/pages/EntryEditorPage.tsx:15-28`、`web/src/pages/EntryEditorPage.tsx:67-75`
- 触发条件：编辑条目 A 但不保存，在组件不卸载的情况下导航到条目 B。
- 结果：`entryId` 会触发新请求，但 `draft`、字段有效性、事件游标和驳回状态不会重置；保存时可能用 B 的 Revision ID 提交 A 的完整草稿。
- 现有保护：请求 generation 只能避免旧 GET 覆盖新响应，`base_revision_id` 也无法判断内容来自另一个条目。
- 修复建议：按 `modelId:entryId` 重新挂载编辑器，或在参数变化时原子重置全部本地状态；存在未保存修改时必须先阻止导航并确认。
- 必要测试：同一 Router 中 A 修改后导航 B，断言 B 不展示或提交 A 的草稿。

## 3. 中风险发现

### M-01 保存操作可重入，新建内容可能产生重复记录

- 位置：`web/src/pages/EntryEditorPage.tsx:67-80`、`web/src/pages/EntryEditorPage.tsx:92`
- 触发条件：新建页面在首个请求返回前双击或重复键盘激活“保存草稿”。
- 结果：两次请求都合法调用创建接口，形成两条独立内容；已有条目则可能出现一次成功、一次冲突的混乱反馈。
- 修复建议：增加同步防重入 ref 和 `saving` 状态；保存、工作流动作在任一写操作期间互斥。

### M-02 编辑页没有未保存修改导航保护

- 位置：`web/src/pages/EntryEditorPage.tsx:38`、`web/src/pages/EntryEditorPage.tsx:92-95`、`web/src/pages/AppShell.tsx:68-70`
- 触发条件：修改后点击返回、侧边导航、刷新或关闭标签页。
- 结果：仅存在内存中的草稿直接丢失。
- 修复建议：同时接入 React Router blocker 和 `beforeunload`；保存成功后解除阻止。

### M-03 旧会话请求可清空或覆盖刚建立的新会话

- 位置：`web/src/auth/AuthProvider.tsx:12-29`、`web/src/api/client.ts:96-98`
- 触发条件：启动时旧 `/auth/session` 请求慢于登录请求返回。
- 结果：旧请求晚到的 401 会无条件 `authStore.clear()`；旧请求晚到的 200 则会把内存 CSRF Token 覆盖成与当前 Cookie 不匹配的旧值。
- 现有保护：`active` 只判断组件是否卸载，不判断认证状态是否已进入新代次。
- 修复建议：认证状态增加 epoch；请求只允许修改发起时对应的 epoch；401 清理前确认请求使用的会话快照仍是当前会话。

### M-04 模型归档未保护入站关联

- 位置：`internal/schema/service.go:154-166`、`internal/schema/service.go:648-650`、`internal/content/service.go:579-599`
- 触发条件：模型 A 的 active 关联字段指向模型 B，随后归档 B。
- 结果：A 的字段仍 active，内容层也可继续写入指向 B 的关系；读取时原始 ID 和展开结果可能长期不一致。
- 修复建议：归档模型时在同一事务锁定并拒绝 active 入站关联；内容写入层防御性要求所有关系目标模型 active。

### M-05 归档嵌套字段仍从内容 API 返回

- 位置：`internal/content/published_reader.go:135`、`internal/content/published_reader.go:387-412`、`internal/content/service_test.go:739-749`
- 触发条件：发布内容包含对象或重复组的子字段，发布后仅归档该子字段。
- 结果：公开 Schema 已删除子字段，但响应投影只过滤根字段，仍返回历史子字段值。
- 修复建议：按 active 字段树递归投影对象和重复组；修改当前把泄漏行为固定为预期的测试。

### M-06 内容和 Schema JSON 上限可在精确边界被绕过

- 位置：`internal/content/http.go:350-357`、`internal/schema/http.go:230-237`
- 触发条件：前 1 MiB 恰好组成合法 JSON，之后继续追加任意字节。
- 结果：`io.LimitReader` 在上限处伪装 EOF，第一次和第二次 Decode 均无法看到后续字节，请求被接受。
- 修复建议：使用 `http.MaxBytesReader` 并识别 `*http.MaxBytesError` 返回 413；增加等于上限和超 1 字节测试。

### M-07 工作流响应违反 OpenAPI Schema

- 位置：`api/openapi/fragments/admin/content/workflow-schemas.yaml:60-82`、`internal/content/types.go:61-93`、`web/src/api/client.ts:157-160`
- 触发条件：成功调用 submit、approve、reject 或 unpublish。
- 结果：OpenAPI 的 `WorkflowEntry` 设置 `additionalProperties: false`，但运行时 `content.Entry` 还返回 `current_draft_content`、`referenced_assets`，特定情况下可能返回 `expanded`；严格响应校验和生成客户端会拒绝响应。
- 修复建议：工作流响应直接引用统一的 `ContentEntry` Schema，并增加实际响应对聚合 OpenAPI 的契约测试。

### M-08 删除 OIDC 后仍接受历史 OIDC 会话

- 位置：`db/migrations/000042_sessions_sms_auth_method.up.sql:1`、`internal/auth/store.go:194-207`、`internal/auth/service.go:193-213`、`web/src/api/types.ts:82`
- 触发条件：升级前数据库保留未过期、未撤销的 `auth_method='oidc'` 会话。
- 结果：迁移仍保留 OIDC 枚举，运行时不校验认证方式，会返回新契约不允许的 `oidc`，且旧会话继续具备原权限。
- 修复建议：新增向前迁移撤销全部 OIDC 会话，再收窄枚举；运行时拒绝未知认证方式。不得修改已有 000042。

### M-09 开发短信模式可与公网 HTTPS 配置组合

- 位置：`internal/platform/config/config.go:188-232`、`cmd/cms/main.go:216-224`
- 触发条件：设置 `APP_ENV=development`、`SMS_PROVIDER=fixed`，同时把 `APP_BASE_URL` 配为公网 HTTPS 并监听非回环地址。
- 结果：服务会正常启动，固定验证码成为公网认证后门；`APP_ENV` 只是部署者可输入的字符串。
- 修复建议：fixed provider 必须同时要求监听和 Base URL 都是 localhost/回环地址；更稳妥的是从生产构建排除 fixed provider。

### M-10 反向代理后认证限流会退化为共享 IP 限流

- 位置：`internal/auth/http.go:303-315`、`internal/auth/service.go:49-55`、`internal/auth/service.go:159-167`
- 触发条件：TLS 网关转发请求，应用只看到网关 `RemoteAddr`。
- 结果：所有公网用户共享一个 IP 限流桶；未认证攻击者可以耗尽 CAPTCHA 或本地登录窗口，持续阻断全部用户。
- 修复建议：配置显式可信代理 CIDR，仅对可信直接对端解析标准转发头；组合真实 IP、主体和全局保护，避免单个共享桶成为拒绝服务开关。

### M-11 同步导出没有总量和并发治理，且最终复制错误被忽略

- 位置：`internal/transfer/service.go:112-146`、`internal/transfer/http.go:147-175`、`cmd/cms/main.go:202`
- 触发条件：低速客户端或多个有 `content.view` 的用户并发导出大模型。
- 结果：每个请求生成完整临时文件，无总行数、字节数和并发限制；全局 30 秒写超时可能截断响应，`io.Copy` 错误未处理。
- 修复建议：增加行数、字节数、用户级和进程级并发上限；设置 `Content-Length` 并记录复制失败；大导出迁移到受控后台任务或对象存储。

## 4. 低风险与结构性问题

### L-01 迁移装载器没有执行连续编号约束

- 位置：`internal/platform/migrate/migrate.go:37-73`
- 现状：当前 42 个迁移实际连续，但 `Load` 只检查格式和重复，不拒绝 `000001, 000003`。
- 修复建议：排序后要求版本严格等于 `index+1`，补首号错误和中间缺号测试。

### L-02 Revision 恰好一页时仍返回下一页游标

- 位置：`internal/content/service.go:1076-1086`
- 触发条件：总数恰好等于 `limit`。
- 结果：条件使用 `len(items) >= limit`，客户端会发起一次无意义的空页请求。
- 修复建议：与其他列表一致改为 `len(items) > limit`，覆盖 `limit-1/limit/limit+1`。

### L-03 必需仓储能力通过可选类型断言静默降级

- 位置：`internal/content/repository.go:31-48`、`internal/content/service.go:525`、`internal/content/service.go:652-679`
- 现状：仓储不实现 `F2Repository` 时，部分写入会静默跳过投影和关联派生数据，另一些路径才在运行时报错。
- 修复建议：F2 已是正式业务语义，应合并必需接口或在构造时校验并拒绝启动，不允许业务写入静默成功。

## 5. 工程与文档债务

以下项目未单列为运行时缺陷，但会降低交付可靠性：

1. `README.md:3`、`README.md:24-30` 仍宣称存在持久化后台任务、多 Worker、续租和取消；当前 CSV 已改为同步实现，文档会误导容量和部署设计。
2. `Makefile` 没有统一的 `verify`、真实 MySQL 迁移门禁和可追踪镜像构建入口；仓库也没有 CI workflow。
3. `Dockerfile` 支持版本注入，但 README 的官方构建命令不传 `VERSION`、`COMMIT`、`BUILD_TIME`，默认二进制只能报告 `dev/unknown`。
4. Docker 基础镜像只固定标签，没有固定 digest；同一提交无法保证供应链层面的可重复构建。
5. `make dev` 只检查 `web/node_modules` 是否存在，锁文件变化后可能继续使用旧依赖。
6. MySQL DDL 成功但 dirty 状态清理前崩溃时，系统会正确 fail-closed，但没有受控 repair 命令和恢复文档。
7. 认证挑战、限流和会话表未发现生产 TTL 清理任务，长期运行会持续增长。
8. 管理 SPA 未统一设置 `frame-ancestors 'none'`、`X-Frame-Options: DENY` 等防嵌入响应头；认证会话响应也应显式 `Cache-Control: no-store, private`。
9. `UsersPage` 和 `AuditPage` 丢弃后端 `next_cursor`，超过默认 20 条的数据无法从管理界面访问。
10. 动态表单多数可见标签没有通过 `htmlFor`、`aria-labelledby` 和 `aria-describedby` 与控件关联。

## 6. 正向审计结果

本次未发现以下高风险问题：

- 未发现可利用 SQL 注入；动态条件使用固定表达式和参数绑定。
- 未发现普通请求可控 SSRF；S3 Endpoint 来自启动配置且执行 HTTPS origin 校验。
- 未发现用户可控对象键或目录穿越；对象键由服务生成并在 S3 层再次校验。
- 管理写请求统一执行会话、Origin 和 CSRF 校验；CSRF Token 使用会话随机值与 HMAC 派生。
- Session 和 API Key 原始 Secret 均不明文落库；比较使用常量时间实现。
- CSV 导出已处理电子表格公式注入。
- 运行时 68 个 HTTP 方法路由与两份聚合 OpenAPI 的方法/路径集合一致。
- 当前 42 个迁移文件编号连续，每个文件保持单语句约束，并受 SHA-256 校验和、dirty、未知版本和 advisory lock 保护。
- 容器使用多阶段构建和非 root 最终用户；Go 和 npm 应用依赖均有锁文件。

## 7. 验证记录

### 通过

```text
go test -count=1 -timeout 300s ./...
go test -race -count=1 -timeout 360s ./...
go vet ./...
cd web && npm run lint
cd web && npm run typecheck
cd web && npm run build
cd web && VITE_ASSETS_ENABLED=false npm run build
```

OpenAPI lint 有 3 条 `operation-2xx-response` warning，均来自按设计只返回 302 的素材下载操作；文档验证本身通过。

使用独立临时 MySQL 8.0.43 容器完成：

```text
空库执行 cms migrate -> 通过
同一数据库第二次执行 cms migrate -> 通过
```

### 未完全通过

```text
cd web && npm run test
```

结果：20 个测试文件中 19 个通过、1 个失败；93 个测试中 92 个通过、1 个失败，并出现 1 个未处理异步异常。

- 失败：`src/App.test.tsx:32`，全量并行运行时登录页挂载节点为空。
- 异常：来源标记为 `src/components/TransferActions.test.tsx`，测试环境销毁后仍有 React scheduler 回调访问 `window`。
- 单独执行 `npm run test -- src/App.test.tsx` 时 8 个测试全部通过。

该结果说明存在测试隔离或异步清理抖动，不能视为完整绿色门禁。应先修复未等待的异步工作、定时器或组件卸载清理，再判断是否需要调整测试并发和等待预算。

### 未执行

- Docker 镜像构建：环境没有 `docker` CLI；本次 MySQL 验证使用 Podman，不以此替代完整镜像 smoke test。
- S3 兼容服务运行时探针：需要真实 Bucket 和凭证，不在本次静态代码审计范围。

## 8. 修复路线

### P0：阻断权限扩张

1. 重构角色授权边界，禁止自授权和超范围授权。
2. 限制手机号凭证替换，保护高权限和应急管理员账户。
3. 限制 API Key 模型 scope 不得超过创建者权限。
4. 为上述三类攻击路径补服务层和 HTTP 回归测试。

### P1：数据一致性和可用性

1. 建立 Schema 兼容性策略。
2. 修复两处事务外连接访问及小连接池并发测试。
3. 隔离编辑器条目状态，增加保存防重入和离开确认。
4. 修复认证 epoch 竞态。
5. 保护模型归档入站关系并递归裁剪归档字段。

### P2：契约和资源治理

1. 统一工作流响应 Schema，处理历史 OIDC 会话。
2. 修复请求体上限边界。
3. 为导出、素材确认和内容 API 增加限流、并发与总量预算。
4. 修复前端游标分页、权限组合和可访问性。

### P3：工程门禁

1. 增加唯一 `make verify`，统一 Go、前端、双模式构建、MySQL 迁移和镜像 smoke。
2. 修复前端测试异步泄漏并接入 CI。
3. 更新 README 的同步 CSV 架构和发布流程。
4. 增加迁移连续性、dirty 恢复流程和容器版本可追踪性。

## 9. 审计签署

```text
scope:            全仓，覆盖后端、前端、契约、迁移和工程配置
review depth:     deep
hard stops:       3 类授权边界问题，生产发布前必须修复
specialists:      architecture, security, frontend, contract/migration
verification:     Go 普通/race/vet 通过；lint/typecheck/build 通过；MySQL 迁移通过；前端全量测试未完全通过
doc debt:         README 同步 CSV 架构、统一验证入口、迁移恢复流程
```
