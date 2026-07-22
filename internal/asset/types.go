package asset

import (
	"context"
	"io"
	"time"

	"cms/internal/platform/database"
)

type Status string

type PreviewKind string
type ContentDisposition string

const (
	StatusQuarantined     Status             = "quarantined"
	StatusAvailable       Status             = "available"
	StatusArchived        Status             = "archived"
	PreviewImage          PreviewKind        = "image"
	PreviewPDF            PreviewKind        = "pdf"
	PreviewVideo          PreviewKind        = "video"
	PreviewAudio          PreviewKind        = "audio"
	PreviewText           PreviewKind        = "text"
	PreviewNone           PreviewKind        = "none"
	DispositionInline     ContentDisposition = "inline"
	DispositionAttachment ContentDisposition = "attachment"
)

type Asset struct {
	ID          string      `json:"id"`
	Filename    string      `json:"filename"`
	MimeType    string      `json:"mime_type"`
	Size        int64       `json:"size"`
	SHA256      string      `json:"sha256"`
	ETag        *string     `json:"etag"`
	Status      Status      `json:"status"`
	PreviewKind PreviewKind `json:"preview_kind"`
	CreatedBy   string      `json:"created_by"`
	CreatedAt   time.Time   `json:"created_at"`
	ConfirmedAt *time.Time  `json:"confirmed_at"`
	ArchivedAt  *time.Time  `json:"archived_at"`
	ObjectKey   string      `json:"-"`
	UploadUntil time.Time   `json:"-"`
}

type SignPutRequest struct {
	ObjectKey   string
	ContentType string
	Size        int64
	SHA256      string
	ExpiresAt   time.Time
}

type SignGetRequest struct {
	ObjectKey        string
	DownloadFilename string
	Disposition      ContentDisposition
	ContentType      string
	ExpiresAt        time.Time
}

type PutObjectRequest struct {
	ObjectKey   string
	ContentType string
	Size        int64
	SHA256      string
}

type SignedRequest struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

type ObjectMetadata struct {
	ObjectKey    string
	Size         int64
	ContentType  string
	SHA256       string
	ETag         string
	LastModified time.Time
}

type ObjectStore interface {
	SignPut(context.Context, SignPutRequest) (SignedRequest, error)
	Head(context.Context, string) (ObjectMetadata, error)
	SignGet(context.Context, SignGetRequest) (SignedRequest, error)
	Put(context.Context, PutObjectRequest, io.Reader) (ObjectMetadata, error)
	Get(context.Context, string) (io.ReadCloser, ObjectMetadata, error)
	Delete(context.Context, string) error
}

type CreateUploadRequest struct {
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

type Upload struct {
	Asset  Asset         `json:"asset"`
	Upload SignedRequest `json:"upload"`
}

type ListQuery struct {
	Status   *Status
	MimeType string
	Limit    int
	Cursor   string
}

type List struct {
	Items      []Asset `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

type Reference struct {
	RevisionID  string
	EntryID     string
	ModelID     string
	FieldID     string
	AssetID     string
	JSONPointer string
	Position    int
}

// ReferenceManager 是内容事务访问素材引用的唯一边界。
type ReferenceManager interface {
	ValidateAvailable(context.Context, database.Querier, []string) error
	InsertRevisionReferences(context.Context, database.Querier, []Reference) error
	ValidatePublishableRevision(context.Context, database.Querier, string) error
}

type PublishedDownloadScope struct {
	AllowedModelIDs []string
}

type PublishedDownload struct {
	ObjectKey string
	Filename  string
}
