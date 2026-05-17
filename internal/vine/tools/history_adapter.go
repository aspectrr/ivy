package tools

import (
	"context"

	"github.com/aspectrr/ivy/internal/vine/history"
)

// HistoryStoreAdapter adapts history.Store to implement HistorySearcher.
type HistoryStoreAdapter struct {
	Store *history.Store
}

// SearchByText implements HistorySearcher.
func (a *HistoryStoreAdapter) SearchByText(ctx context.Context, query string, limit int) ([]HistoryResult, error) {
	entries, err := a.Store.SearchByText(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryResult, len(entries))
	for i, e := range entries {
		out[i] = HistoryResult{
			SessionID: e.SessionID,
			Content:   e.Content,
			CreatedAt: e.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}
	return out, nil
}
