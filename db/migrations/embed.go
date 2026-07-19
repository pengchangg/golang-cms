package migrations

import "embed"

// Files 包含与当前二进制严格匹配的数据库迁移。
//
//go:embed *.up.sql
var Files embed.FS
