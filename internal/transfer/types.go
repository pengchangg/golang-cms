package transfer

import (
	"context"

	"cms/internal/content"
	"cms/internal/identity"
	"cms/internal/platform/database"
)

type TransferError struct {
	Row     int    `json:"row"`
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ImportResult struct {
	Imported int `json:"imported"`
}

type ExportRequest struct {
	WorkflowStatus string
	Filter         string
	RelationFilter string
	Sort           string
}

type ExportQuery struct {
	WorkflowStatus  *content.WorkflowStatus
	Filters         []content.PublishedFilter
	RelationFilters []content.PublishedRelationFilter
	Sort            []content.PublishedSort
}

type Importer interface {
	ImportDrafts(context.Context, identity.Principal, content.RequestMeta, string, content.DraftSource, func(database.Querier) error) error
}

type EntryLister interface {
	ListEntries(context.Context, identity.Principal, string, content.AdminEntryQuery) (content.EntryList, error)
}
