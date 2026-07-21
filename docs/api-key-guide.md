# API Key 使用指南

API Key 用于内部客户端读取 CMS 中已经发布的内容。它只能访问 `/api/content/v1`，不能代替管理会话调用 `/api/admin/v1`。

## 1. 认证方式区别

| API | 用途 | 认证方式 |
| --- | --- | --- |
| `/api/admin/v1` | 模型、内容、审核和 API Key 管理 | `cms_session` Cookie；写请求还需同源 `Origin` 和 `X-CSRF-Token` |
| `/api/content/v1` | 客户端只读已发布内容 | `Authorization: Bearer <api-key>` |

管理 API 返回 `session_invalid` 时，应重新建立管理会话。不要把 API Key 放入管理 API 请求。

## 2. 创建 API Key

登录管理端后打开 `/api-keys`：

1. 点击“创建 API Key”。
2. 填写便于识别的名称。
3. 选择允许读取的内容模型。
4. 按需设置过期时间；不设置表示永不过期。
5. 创建后立即将完整 Key 保存到受控的密钥管理系统。

完整 Key 格式为：

```text
cmsk_<12位前缀>_<43位secret>
```

完整 Key 只在创建或轮换成功后展示一次。数据库只保存 prefix、独立 salt 和 secret hash，关闭窗口后无法恢复原 Key。遗失时应轮换或重新创建。

不要把完整 Key 写入仓库、日志、审计、工单、聊天记录或浏览器持久化存储。本文中的 Key 均为格式示例，不是有效凭证。

## 3. 基本调用

以下示例使用本地开发地址：

```bash
BASE_URL='http://localhost:18080'
API_KEY='cmsk_这里替换为创建后得到的完整Key'
```

请求头必须使用精确格式：

```http
Authorization: Bearer cmsk_...
```

### 3.1 列出授权模型

```bash
curl --fail-with-body \
  --header "Authorization: Bearer $API_KEY" \
  "$BASE_URL/api/content/v1/models"
```

该接口只返回 API Key 授权范围内仍处于 active 状态的模型：

```json
{
  "items": [
    {
      "id": "mdl_xxx",
      "key": "articles",
      "display_name": "文章",
      "description": "官网文章",
      "updated_at": "2026-07-20T08:00:00Z"
    }
  ]
}
```

后续内容 API 路径使用稳定模型 `key`，例如 `articles`，不是内部的 `mdl_xxx` ID。

### 3.2 获取模型定义

```bash
MODEL_KEY='articles'

curl --fail-with-body \
  --header "Authorization: Bearer $API_KEY" \
  "$BASE_URL/api/content/v1/models/$MODEL_KEY"
```

响应包含模型信息和 active 字段，可用于构造客户端渲染或数据映射逻辑。

### 3.3 获取已发布内容列表

```bash
curl --fail-with-body \
  --header "Authorization: Bearer $API_KEY" \
  "$BASE_URL/api/content/v1/models/$MODEL_KEY/entries"
```

内容 API 只读取当前发布 Revision。草稿、待审核、驳回、下线和归档内容不会返回。

典型响应：

```json
{
  "items": [
    {
      "id": "ent_xxx",
      "model_id": "mdl_xxx",
      "model_key": "articles",
      "revision_id": "rev_xxx",
      "revision_number": 3,
      "content": {
        "title": "API Key 使用指南",
        "score": 95
      },
      "expanded": {},
      "published_at": "2026-07-20T08:00:00Z",
      "updated_at": "2026-07-20T07:55:00Z"
    }
  ],
  "next_cursor": null
}
```

### 3.4 获取单条已发布内容

```bash
ENTRY_ID='ent_xxx'

curl --fail-with-body \
  --header "Authorization: Bearer $API_KEY" \
  "$BASE_URL/api/content/v1/models/$MODEL_KEY/entries/$ENTRY_ID"
```

条目不存在、已归档、未发布或已下线时返回 `404 published_content_not_found`。

## 4. 分页、过滤和排序

### 4.1 分页

`limit` 默认 20，允许范围为 1 至 100：

```bash
curl --fail-with-body \
  --get \
  --header "Authorization: Bearer $API_KEY" \
  --data-urlencode 'limit=20' \
  "$BASE_URL/api/content/v1/models/$MODEL_KEY/entries"
```

若响应中的 `next_cursor` 非空，将其原样传入下一页：

```bash
NEXT_CURSOR='服务端返回的不透明游标'

curl --fail-with-body \
  --get \
  --header "Authorization: Bearer $API_KEY" \
  --data-urlencode 'limit=20' \
  --data-urlencode "cursor=$NEXT_CURSOR" \
  "$BASE_URL/api/content/v1/models/$MODEL_KEY/entries"
```

游标绑定模型、Key 范围、过滤、排序、展开和页大小。改变这些条件后不能继续使用旧游标，否则返回 `400 invalid_cursor`。

### 4.2 标量过滤

`filter` 是 URL 编码的 JSON object，最多包含 5 个声明为 `filterable=true` 的根级标量字段。

```bash
curl --fail-with-body \
  --get \
  --header "Authorization: Bearer $API_KEY" \
  --data-urlencode 'filter={"score":{"gte":80},"featured":{"eq":true}}' \
  "$BASE_URL/api/content/v1/models/$MODEL_KEY/entries"
```

支持的操作符：

| 字段类型 | 操作符 |
| --- | --- |
| 文本、单选、布尔 | `eq`、`ne`、`in` |
| 整数、小数、日期、日期时间 | `eq`、`ne`、`gt`、`gte`、`lt`、`lte`、`in` |

`in` 接受 1 至 20 个无重复同类型值。小数值使用字符串，例如：

```bash
--data-urlencode 'filter={"price":{"lt":"99.50"}}'
```

### 4.3 关联过滤

`relation_filter` 最多包含 2 个根级关联字段，操作符固定为 `contains`：

```bash
curl --fail-with-body \
  --get \
  --header "Authorization: Bearer $API_KEY" \
  --data-urlencode 'relation_filter={"author":{"contains":"ent_author_xxx"}}' \
  "$BASE_URL/api/content/v1/models/$MODEL_KEY/entries"
```

### 4.4 排序

`sort` 最多包含 3 项，`-` 前缀表示降序。动态字段必须声明 `sortable=true`；`published_at` 和 `id` 始终可排序。

```bash
curl --fail-with-body \
  --get \
  --header "Authorization: Bearer $API_KEY" \
  --data-urlencode 'sort=-score,-published_at' \
  "$BASE_URL/api/content/v1/models/$MODEL_KEY/entries"
```

默认排序为 `-published_at,-id`。服务端会使用 `id` 作为稳定决胜键。

## 5. 展开关联

`expand` 最多指定 3 个根级单关联或多关联字段，只展开一层：

```bash
curl --fail-with-body \
  --get \
  --header "Authorization: Bearer $API_KEY" \
  --data-urlencode 'expand=author,reviewers' \
  "$BASE_URL/api/content/v1/models/$MODEL_KEY/entries"
```

关联目标模型也必须在当前 API Key 的模型范围内。未发布、已归档、不存在或超出范围的目标不会出现在 `expanded` 中，原始 `content` 中的关联条目 ID 仍会保留。

综合查询示例：

```bash
curl --fail-with-body \
  --get \
  --header "Authorization: Bearer $API_KEY" \
  --data-urlencode 'limit=10' \
  --data-urlencode 'filter={"score":{"gte":80}}' \
  --data-urlencode 'relation_filter={"author":{"contains":"ent_author_xxx"}}' \
  --data-urlencode 'sort=-score,-published_at' \
  --data-urlencode 'expand=author' \
  "$BASE_URL/api/content/v1/models/$MODEL_KEY/entries"
```

## 6. ETag 条件请求

四个 JSON 内容端点成功时返回强 `ETag` 和 `Cache-Control: private, no-cache`。客户端可缓存响应体，并用 `If-None-Match` 验证：

```bash
ETAG='"sha256-服务端返回的值"'

curl --fail-with-body \
  --header "Authorization: Bearer $API_KEY" \
  --header "If-None-Match: $ETAG" \
  "$BASE_URL/api/content/v1/models/$MODEL_KEY/entries"
```

内容未变化时返回 `304 Not Modified`，无响应体。鉴权和查询校验先于 ETag 判断，无效 Key 不会因 ETag 命中而绕过鉴权。

## 7. 素材下载

内容中的媒体字段保存素材 ID。素材功能启用时，可通过以下端点取得短时签名下载地址：

```bash
ASSET_ID='ast_xxx'

curl --silent --show-error \
  --dump-header - \
  --output /dev/null \
  --header "Authorization: Bearer $API_KEY" \
  "$BASE_URL/api/content/v1/assets/$ASSET_ID"
```

成功返回 `302`，`Location` 是短时私有 S3 兼容对象存储签名 URL，响应使用 `Cache-Control: private, no-store`。

只有被 API Key 授权模型的当前发布 Revision 引用的素材才可访问。`make dev` 默认从 `.env.assets.local` 启用对象存储；使用 `make dev DEV_ASSETS_ENABLED=false` 时不会注册素材下载端点。

为避免跨主机重定向时意外转发 API Key，推荐先读取 `Location`，再单独请求签名 URL，而不是无条件让 HTTP 客户端携带认证头跟随重定向。

## 8. 轮换和撤销

管理端 `/api-keys` 页面支持轮换和撤销：

- 轮换会创建一个继承当前配置的新 Key，并立即撤销旧 Key。
- 新完整 Key 仍只展示一次。
- 旧 Key 没有重叠期或宽限期，事务提交后立即返回 `401 api_key_revoked`。
- 撤销立即生效且不可恢复。
- 已过期或已撤销的 Key 不能轮换。

生产客户端轮换建议：

1. 在计划维护窗口创建或轮换 Key。
2. 立即把新 Key 写入密钥管理系统。
3. 原子更新客户端配置并重新加载。
4. 用 `/api/content/v1/models` 验证新 Key。

由于轮换会立即撤销旧 Key，需要无中断切换时，应先创建一个独立的新 Key、完成客户端切换，再撤销旧 Key，而不是直接轮换。

## 9. 错误处理

统一错误格式：

```json
{
  "error": {
    "code": "stable_error_code",
    "message": "错误说明",
    "request_id": "req_xxx",
    "details": []
  }
}
```

客户端应根据稳定的 `error.code` 分支，不应依赖 `message` 文本。

| HTTP | 错误码 | 含义 |
| --- | --- | --- |
| 400 | `invalid_query` | 查询参数、过滤、排序或展开非法 |
| 400 | `invalid_cursor` | 游标无效或查询条件已变化 |
| 401 | `api_key_required` | 缺少 Authorization |
| 401 | `invalid_api_key` | Bearer 格式、prefix 或 secret 无效 |
| 401 | `api_key_expired` | Key 已过期 |
| 401 | `api_key_revoked` | Key 已撤销或已被轮换替代 |
| 404 | `published_model_not_found` | 模型不存在、已归档或不在 Key 范围 |
| 404 | `published_content_not_found` | 条目不存在、已归档或没有当前发布 Revision |
| 404 | `published_asset_not_found` | 素材不存在或没有授权的当前发布引用 |
| 503 | `object_store_unavailable` | 对象存储暂时不可用 |

遇到错误时保留 `request_id` 供服务端排查，但不要记录完整 API Key。

## 10. 安全建议

- 每个客户端使用独立 Key，不要跨系统共享。
- 只授权客户端实际需要的模型。
- 生产 Key 设置合理过期时间并建立轮换计划。
- 使用密钥管理服务注入 Key，不要硬编码到源码或镜像。
- 不要把服务端 Key 下发到不受信任的浏览器或移动客户端。
- 发现泄露后立即撤销，并检查 API Key 的最近使用时间和审计记录。
- API Key 只提供读取已发布内容的能力，不应拥有管理端权限。

完整机器可读契约见 [`api/openapi/content.yaml`](../api/openapi/content.yaml) 和 [`api/openapi/admin.yaml`](../api/openapi/admin.yaml)。
