// Package pagination applies the API's page, limit, and search rules to list queries.
package pagination

import (
	"strings"

	"github.com/aifgrouplaos/candidate-api/pkg/apierror"
)

const (
	DefaultLimit = 20
	MaxLimit     = 100
	// MaxPage bounds offsets so a query cannot force an unbounded database scan.
	MaxPage = 10_000
)

// Query holds the paging and search parameters every list endpoint accepts.
type Query struct {
	Page   int    `query:"page"`
	Limit  int    `query:"limit"`
	Search string `query:"search"`
}

// Check adds errors for an over-long search or too-large page to v and returns the trimmed search.
func (q Query) Check(v *apierror.FieldErrors) string {
	search := strings.TrimSpace(q.Search)
	v.Length("search", search, 0, 100, "Search must be at most 100 characters.")
	if q.Page > MaxPage {
		v.Add("page", "Page is too large.")
	}
	return search
}

type Meta struct {
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"totalPages"`
}

// Bounds returns page and limit with defaults applied and limit capped, plus the row offset.
// Callers reject the query with Check first.
func (q Query) Bounds() (page, limit, offset int) {
	page, limit = max(q.Page, 1), q.Limit
	if limit < 1 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	return page, limit, (page - 1) * limit
}

func NewMeta(page, limit int, total int64) Meta {
	return Meta{Page: page, Limit: limit, Total: total, TotalPages: int((total + int64(limit) - 1) / int64(limit))}
}
