package transfer

import "cms/internal/platform/apperror"

type jobFileExpiredError struct{}

func (*jobFileExpiredError) Error() string { return "任务文件已过期" }

func invalid(code, message string) error {
	return &apperror.Error{Kind: apperror.KindInvalidArgument, Code: code, Message: message}
}
func conflict(code, message string) error {
	return &apperror.Error{Kind: apperror.KindConflict, Code: code, Message: message}
}
func notFound() error {
	return &apperror.Error{Kind: apperror.KindNotFound, Code: "resource_not_found", Message: "任务或资源不存在"}
}
func forbidden() error {
	return &apperror.Error{Kind: apperror.KindPermissionDenied, Code: "permission_denied", Message: "权限不足"}
}
func objectStoreUnavailable(cause error) error {
	return &apperror.Error{Kind: apperror.KindUnavailable, Code: "object_store_unavailable", Message: "对象存储暂时不可用", Cause: cause}
}
