package apperror

type Kind uint8

const (
	KindInvalidArgument Kind = iota + 1
	KindUnauthenticated
	KindPermissionDenied
	KindNotFound
	KindConflict
	KindUnavailable
	KindInternal
)

type Error struct {
	Kind    Kind
	Code    string
	Message string
	Details []map[string]any
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Cause }
