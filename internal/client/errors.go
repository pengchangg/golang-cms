package client

import (
	"cms/internal/platform/apperror"
)

func appError(kind apperror.Kind, code, message string) error {
	return &apperror.Error{Kind: kind, Code: code, Message: message}
}

func validation(details ...map[string]any) error {
	return &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "validation_failed", Message: "请求数据校验失败", Details: details}
}

func invalidQuery() error {
	return appError(apperror.KindInvalidArgument, "invalid_query", "内容查询无效")
}

func invalidCursor() error {
	return appError(apperror.KindInvalidArgument, "invalid_cursor", "分页游标无效")
}

func invalidAPIKey() error {
	return appError(apperror.KindUnauthenticated, "invalid_api_key", "API Key 无效")
}
