package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"

	"cms/internal/content"
)

var allowedContentQuery = map[string]bool{"limit": true, "cursor": true, "filter": true, "relation_filter": true, "sort": true, "expand": true}

func parsePublishedQuery(values url.Values) (content.PublishedQuery, error) {
	for key, items := range values {
		if !allowedContentQuery[key] || len(items) != 1 {
			return content.PublishedQuery{}, invalidQuery()
		}
	}
	result := content.PublishedQuery{Limit: 20, Cursor: values.Get("cursor")}
	if raw, ok := values["limit"]; ok {
		limit, err := strconv.Atoi(raw[0])
		if err != nil || limit < 1 || limit > 100 {
			return result, invalidQuery()
		}
		result.Limit = limit
	}
	filters, err := parseFilters(values.Get("filter"))
	if err != nil {
		return result, err
	}
	result.Filters = filters
	relations, err := parseRelationFilters(values.Get("relation_filter"))
	if err != nil {
		return result, err
	}
	result.RelationFilters = relations
	sortValues, err := parseSort(values.Get("sort"))
	if err != nil {
		return result, err
	}
	result.Sort = sortValues
	expand, err := parseCSV(values.Get("expand"), 3)
	if err != nil {
		return result, err
	}
	result.Expand = expand
	return result, nil
}

func parseFilters(raw string) ([]content.PublishedFilter, error) {
	if raw == "" {
		return nil, nil
	}
	object, err := decodeRawObject(raw)
	if err != nil || len(object) > 5 {
		return nil, invalidQuery()
	}
	result := make([]content.PublishedFilter, 0, len(object))
	for field, value := range object {
		operators, err := decodeRawObject(string(value))
		if err != nil || len(operators) != 1 || field == "" {
			return nil, invalidQuery()
		}
		for operator, operand := range operators {
			if !validOperator(operator) || !validSingleJSON(operand) {
				return nil, invalidQuery()
			}
			result = append(result, content.PublishedFilter{FieldKey: field, Operator: operator, Value: append(json.RawMessage(nil), operand...)})
		}
	}
	sortFilters(result)
	return result, nil
}

func parseRelationFilters(raw string) ([]content.PublishedRelationFilter, error) {
	if raw == "" {
		return nil, nil
	}
	object, err := decodeRawObject(raw)
	if err != nil || len(object) > 2 {
		return nil, invalidQuery()
	}
	result := make([]content.PublishedRelationFilter, 0, len(object))
	for field, value := range object {
		operation, err := decodeRawObject(string(value))
		if err != nil || len(operation) != 1 {
			return nil, invalidQuery()
		}
		rawID, ok := operation["contains"]
		if !ok {
			return nil, invalidQuery()
		}
		var id string
		if json.Unmarshal(rawID, &id) != nil || id == "" || field == "" {
			return nil, invalidQuery()
		}
		result = append(result, content.PublishedRelationFilter{FieldKey: field, EntryID: id})
	}
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j].FieldKey < result[j-1].FieldKey; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	return result, nil
}

func parseSort(raw string) ([]content.PublishedSort, error) {
	parts, err := parseCSV(raw, 3)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	result := make([]content.PublishedSort, len(parts))
	for i, value := range parts {
		descending := strings.HasPrefix(value, "-")
		field := strings.TrimPrefix(value, "-")
		if field == "" || strings.HasPrefix(field, "-") || seen[field] {
			return nil, invalidQuery()
		}
		seen[field] = true
		result[i] = content.PublishedSort{FieldKey: field, Descending: descending}
	}
	return result, nil
}

func parseCSV(raw string, max int) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > max {
		return nil, invalidQuery()
	}
	seen := map[string]bool{}
	for _, part := range parts {
		if part == "" || strings.TrimSpace(part) != part || seen[part] {
			return nil, invalidQuery()
		}
		seen[part] = true
	}
	return parts, nil
}
func decodeRawObject(raw string) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, invalidQuery()
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, invalidQuery()
	}
	return result, nil
}
func validSingleJSON(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return false
	}
	return errors.Is(decoder.Decode(&struct{}{}), io.EOF)
}
func validOperator(value string) bool {
	switch value {
	case "eq", "ne", "gt", "gte", "lt", "lte", "in":
		return true
	}
	return false
}
func sortFilters(items []content.PublishedFilter) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].FieldKey < items[j-1].FieldKey; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
