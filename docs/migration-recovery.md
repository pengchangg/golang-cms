# 迁移故障恢复手册

## 适用范围

本手册用于 `cms migrate` 或 `cms serve` 报告迁移处于 `dirty`、历史不连续、未知版本或校验和不匹配时的人工恢复。迁移文件受校验和保护，每个文件只有一条独立 DDL 或 DML；应用不会自动修复迁移状态。

## 通用前置条件

1. 停止所有旧版和新版应用进程，阻断写流量。
2. 对数据库创建可验证的全量备份或存储快照，并记录恢复点。
3. 保留失败时使用的原始二进制、镜像 digest、提交号和完整日志。
4. 使用只读 SQL 检查 `schema_migrations`，不得先修改状态：

```sql
SELECT version, name, checksum, dirty, applied_at
FROM schema_migrations
ORDER BY version;
```

## Dirty 恢复

`dirty=TRUE` 表示状态行已写入，但迁移语句或完成标记未得到确认。MySQL DDL 可能隐式提交，不能仅凭客户端报错判断语句是否执行。

1. 从失败时的同一提交取得对应迁移文件，运行 `sha256sum db/migrations/NNNNNN_name.up.sql`，确认文件名和哈希与状态行完全一致。
2. 根据迁移 SQL 使用 `information_schema`、目标表结构和业务数据检查语句效果。
3. 若能证明迁移语句完全未生效，删除该条 dirty 状态行，然后使用原始二进制重新执行 `cms migrate`：

```sql
DELETE FROM schema_migrations
WHERE version = NNNNNN AND dirty = TRUE AND name = 'NNNNNN_name.up.sql' AND checksum = 'expected_sha256';
```

4. 若能证明迁移语句已完整生效、仅最终状态更新失败，可将该行标记完成：

```sql
UPDATE schema_migrations
SET dirty = FALSE, applied_at = UTC_TIMESTAMP(6)
WHERE version = NNNNNN AND dirty = TRUE AND name = 'NNNNNN_name.up.sql' AND checksum = 'expected_sha256';
```

5. 若语句效果不完整、无法证明结果，或 DML 可能只处理了部分数据，立即恢复备份，不得删除 dirty 行或强制标记完成。
6. 恢复后使用同一发布二进制连续执行两次 `cms migrate`，再启动应用。

所有手工 SQL 必须将示例中的版本、文件名和哈希替换为现场值，并经过双人复核。`UPDATE` 或 `DELETE` 影响行数不为 1 时立即停止。

## 未知版本或历史不连续

- 数据库版本高于当前二进制时，部署包含该版本的正确新二进制；不得删除较新迁移记录来迁就旧应用。
- 历史缺少前置版本时，先恢复备份或查明状态表被修改的原因；不得补造状态行。
- 多个环境结果不一致时，以各自备份和执行日志为依据分别处理，不复制其他环境的 `schema_migrations`。

## 校验和不匹配

- 找回执行该迁移时的原始文件或镜像，确认数据库状态行记录的文件名和校验和。
- 已执行迁移不得修改、删除、重排或复用编号。业务修正只能追加新的向前迁移。
- 不得更新数据库校验和来匹配当前工作树，也不得修改当前文件来匹配数据库；先恢复正确构建产物，再评估追加修复迁移。

## 恢复验收

1. `cms migrate` 首次执行成功。
2. `cms migrate` 第二次执行成功且没有新增状态变化。
3. `schema_migrations` 版本从 `000001` 连续到当前版本，全部 `dirty=FALSE`。
4. 使用发布二进制执行 `cms version`，记录版本、提交和构建时间。
5. 启动单个应用实例完成登录、会话、内容读写和受影响功能冒烟测试后，再恢复其余实例和流量。
