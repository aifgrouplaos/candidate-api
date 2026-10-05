package pagination

import "testing"

func TestBoundsAndMeta(t *testing.T) {
	for _, c := range []struct{ page, limit, wantPage, wantLimit, wantOffset int }{
		{0, 0, 1, DefaultLimit, 0},
		{-3, -1, 1, DefaultLimit, 0},
		{3, 10, 3, 10, 20},
		{2, 500, 2, MaxLimit, MaxLimit},
	} {
		page, limit, offset := Bounds(c.page, c.limit)
		if page != c.wantPage || limit != c.wantLimit || offset != c.wantOffset {
			t.Errorf("Bounds(%d, %d) = %d, %d, %d", c.page, c.limit, page, limit, offset)
		}
	}
	for total, want := range map[int64]int{0: 0, 1: 1, 20: 1, 21: 2} {
		if got := NewMeta(1, 20, total).TotalPages; got != want {
			t.Errorf("total %d: %d pages, want %d", total, got, want)
		}
	}
}
