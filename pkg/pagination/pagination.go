// Package pagination applies the API's page and limit rules to list queries.
package pagination

const (
	DefaultLimit = 20
	MaxLimit     = 100
	// MaxPage bounds offsets so a query cannot force an unbounded database scan.
	MaxPage = 10_000
)

type Meta struct {
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"totalPages"`
}

// Bounds returns page and limit with defaults applied and limit capped, plus the row offset.
// Callers reject page > MaxPage first.
func Bounds(queryPage, queryLimit int) (page, limit, offset int) {
	page, limit = max(queryPage, 1), queryLimit
	if limit < 1 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	return page, limit, (page - 1) * limit
}

func NewMeta(page, limit int, total int64) Meta {
	return Meta{Page: page, Limit: limit, Total: total, TotalPages: int((total + int64(limit) - 1) / int64(limit))}
}
