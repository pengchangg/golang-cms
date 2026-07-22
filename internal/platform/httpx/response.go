package httpx

import (
	"encoding/json"
	"errors"
	"net/http"

	"cms/internal/platform/apperror"
)

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string           `json:"code"`
	Message   string           `json:"message"`
	RequestID string           `json:"request_id"`
	Details   []map[string]any `json:"details"`
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	body := errorBody{Code: "internal_error", Message: "服务器内部错误", Details: []map[string]any{}}
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		status = statusForKind(appErr.Kind)
		body.Code = appErr.Code
		body.Message = appErr.Message
		if appErr.Details != nil {
			body.Details = appErr.Details
		}
	}
	body.RequestID = RequestIDFromContext(r.Context())
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: body})
}

func statusForKind(kind apperror.Kind) int {
	switch kind {
	case apperror.KindInvalidArgument:
		return http.StatusBadRequest
	case apperror.KindUnauthenticated:
		return http.StatusUnauthorized
	case apperror.KindPermissionDenied:
		return http.StatusForbidden
	case apperror.KindNotFound:
		return http.StatusNotFound
	case apperror.KindConflict:
		return http.StatusConflict
	case apperror.KindUnavailable:
		return http.StatusServiceUnavailable
	case apperror.KindPayloadTooLarge:
		return http.StatusRequestEntityTooLarge
	case apperror.KindTooManyRequests:
		return http.StatusTooManyRequests
	case apperror.KindMethodNotAllowed:
		return http.StatusMethodNotAllowed
	default:
		return http.StatusInternalServerError
	}
}

func NotFound(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, &apperror.Error{Kind: apperror.KindNotFound, Code: "not_found", Message: "请求的资源不存在"})
}
