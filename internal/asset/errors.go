package asset

import (
	"errors"

	"cms/internal/platform/apperror"
)

var (
	ErrObjectNotFound   = errors.New("对象不存在")
	ErrStoreUnavailable = errors.New("对象存储暂时不可用")
	ErrStoreConfig      = errors.New("对象存储配置错误")
)

func appError(kind apperror.Kind, code, message string) error {
	return &apperror.Error{Kind: kind, Code: code, Message: message}
}

func storeError(err error) error {
	switch {
	case errors.Is(err, ErrObjectNotFound):
		return appError(apperror.KindConflict, "asset_object_missing", "上传对象不存在")
	case errors.Is(err, ErrStoreUnavailable):
		return appError(apperror.KindUnavailable, "object_store_unavailable", "对象存储暂时不可用")
	case errors.Is(err, ErrStoreConfig):
		return appError(apperror.KindInternal, "internal_error", "对象存储配置错误")
	default:
		return appError(apperror.KindInternal, "internal_error", "对象存储操作失败")
	}
}
