package transfer

import "cms/internal/platform/apperror"

func invalid(code, message string) error {
	return &apperror.Error{Kind: apperror.KindInvalidArgument, Code: code, Message: message}
}

func invalidDetail(detail TransferError) error {
	return &apperror.Error{
		Kind:    apperror.KindInvalidArgument,
		Code:    "csv_invalid",
		Message: "CSV 数据无效",
		Details: []map[string]any{{"row": detail.Row, "field": detail.Field, "code": detail.Code, "message": detail.Message}},
	}
}

func forbidden() error {
	return &apperror.Error{Kind: apperror.KindPermissionDenied, Code: "permission_denied", Message: "权限不足"}
}
