package content

import (
	"sort"

	"cms/internal/platform/apperror"
)

type validationDetail struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type validationErrors []validationDetail

func (v *validationErrors) add(path, code, message string) {
	*v = append(*v, validationDetail{Path: path, Code: code, Message: message})
}

func (v validationErrors) err() error {
	if len(v) == 0 {
		return nil
	}
	sort.Slice(v, func(i, j int) bool {
		if v[i].Path == v[j].Path {
			return v[i].Code < v[j].Code
		}
		return v[i].Path < v[j].Path
	})
	details := make([]map[string]any, len(v))
	for i, detail := range v {
		details[i] = map[string]any{"path": detail.Path, "code": detail.Code, "message": detail.Message}
	}
	return &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "validation_failed", Message: "请求数据校验失败", Details: details}
}

func notFound(resource string) error {
	return &apperror.Error{Kind: apperror.KindNotFound, Code: "resource_not_found", Message: resource + "不存在"}
}

func conflict(code, message string) error {
	return &apperror.Error{Kind: apperror.KindConflict, Code: code, Message: message}
}

func invalidCursor() error {
	return &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "invalid_cursor", Message: "分页游标无效"}
}
