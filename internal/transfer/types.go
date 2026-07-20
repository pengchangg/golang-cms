package transfer

import (
	"context"
	"io"
	"time"

	"cms/internal/content"
)

type JobType string
type JobStatus string

const (
	JobCSVImport JobType   = "csv_import"
	JobCSVExport JobType   = "csv_export"
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	JobCanceled  JobStatus = "canceled"
)

type Job struct {
	ID                string     `json:"id"`
	Type              JobType    `json:"type"`
	Status            JobStatus  `json:"status"`
	ModelID           string     `json:"model_id"`
	Progress          int        `json:"progress"`
	Attempt           int        `json:"attempt"`
	MaxAttempts       int        `json:"max_attempts"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at"`
	ErrorCode         *string    `json:"error_code"`
	ErrorMessage      *string    `json:"error_message"`
	CreatedBy         string     `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	ExpiresAt         *time.Time `json:"expires_at"`
	ModelSnapshot     []byte     `json:"-"`
	RequestSnapshot   []byte     `json:"-"`
	SourceObjectKey   string     `json:"-"`
	ResultObjectKey   string     `json:"-"`
	ErrorObjectKey    string     `json:"-"`
	CommittedAt       *time.Time `json:"-"`
	ErrorsTruncated   bool       `json:"-"`
}

type TransferError struct {
	Row     int    `json:"row"`
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type JobList struct {
	Items      []Job   `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
type ErrorList struct {
	Items           []TransferError `json:"items"`
	NextCursor      *string         `json:"next_cursor"`
	ErrorsTruncated bool            `json:"errors_truncated"`
}
type JobFilter struct {
	Type    JobType
	Status  JobStatus
	ModelID string
	Limit   int
	Cursor  string
}

type ExportRequest struct {
	WorkflowStatus string `json:"workflow_status,omitempty"`
	Filter         string `json:"filter,omitempty"`
	RelationFilter string `json:"relation_filter,omitempty"`
	Sort           string `json:"sort,omitempty"`
}

type ExportQuery struct {
	WorkflowStatus  *content.WorkflowStatus
	Filters         []content.PublishedFilter
	RelationFilters []content.PublishedRelationFilter
	Sort            []content.PublishedSort
}

type ObjectStore interface {
	Get(context.Context, string) (io.ReadCloser, error)
	Put(context.Context, string, string, io.Reader) error
	SignGet(context.Context, string, string, time.Time) (string, error)
}

type ImportUpload struct {
	UploadID  string            `json:"upload_id"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

type UploadClaims struct {
	ID, ObjectKey, Filename, SHA256 string
	Size                            int64
	ExpiresAt                       time.Time
}
type UploadManager interface {
	Create(context.Context, string, int64, string, time.Time) (ImportUpload, error)
	Confirm(context.Context, string) (UploadClaims, error)
}

type Download struct{ Location string }
