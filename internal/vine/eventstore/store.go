package eventstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/aspectrr/ivy/internal/vine/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides append-only event operations.
type Store struct {
	pool *pgxpool.Pool

	mu sync.RWMutex
	// watchers maps sessionID → slice of channels that receive new events.
	watchers map[string][]chan model.Event
}

// NewStore creates a new event store backed by the given pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:     pool,
		watchers: make(map[string][]chan model.Event),
	}
}

// Append adds a new event to the session's event log.
// The sequence number is auto-incremented within the session transactionally.
func (s *Store) Append(ctx context.Context, sessionID string, eventType string, data json.RawMessage) (*model.Event, error) {
	if data == nil {
		data = json.RawMessage(`{}`)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Get the next sequence number for this session.
	var nextSeq int64
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(seq), 0) + 1 FROM events WHERE session_id = $1
	`, sessionID).Scan(&nextSeq)
	if err != nil {
		return nil, fmt.Errorf("getting next seq: %w", err)
	}

	var evt model.Event
	err = tx.QueryRow(ctx, `
		INSERT INTO events (session_id, seq, type, data)
		VALUES ($1, $2, $3, $4)
		RETURNING id, session_id, seq, type, data, created_at
	`, sessionID, nextSeq, eventType, data).Scan(
		&evt.ID, &evt.SessionID, &evt.Seq, &evt.Type, &evt.Data, &evt.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing event: %w", err)
	}

	// Notify watchers after commit.
	s.notifyWatchers(evt)

	return &evt, nil
}

// GetEvents returns events for a session, starting after the given sequence number.
// Use limit for pagination. Pass afterSeq=0 to start from the beginning.
func (s *Store) GetEvents(ctx context.Context, sessionID string, afterSeq int64, limit int) ([]model.Event, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, session_id, seq, type, data, created_at
		FROM events
		WHERE session_id = $1 AND seq > $2
		ORDER BY seq ASC
		LIMIT $3
	`, sessionID, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("querying events: %w", err)
	}
	defer rows.Close()

	var events []model.Event
	for rows.Next() {
		var evt model.Event
		if err := rows.Scan(
			&evt.ID, &evt.SessionID, &evt.Seq, &evt.Type, &evt.Data, &evt.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		events = append(events, evt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating events: %w", err)
	}
	return events, nil
}

// GetLatest returns the most recent event for a session.
func (s *Store) GetLatest(ctx context.Context, sessionID string) (*model.Event, error) {
	var evt model.Event
	err := s.pool.QueryRow(ctx, `
		SELECT id, session_id, seq, type, data, created_at
		FROM events
		WHERE session_id = $1
		ORDER BY seq DESC
		LIMIT 1
	`, sessionID).Scan(
		&evt.ID, &evt.SessionID, &evt.Seq, &evt.Type, &evt.Data, &evt.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("getting latest event: %w", err)
	}
	return &evt, nil
}

// GetEventsByType returns events of a specific type for a session, newest first.
func (s *Store) GetEventsByType(ctx context.Context, sessionID string, eventType string, limit int) ([]model.Event, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, session_id, seq, type, data, created_at
		FROM events
		WHERE session_id = $1 AND type = $2
		ORDER BY seq DESC
		LIMIT $3
	`, sessionID, eventType, limit)
	if err != nil {
		return nil, fmt.Errorf("querying events by type: %w", err)
	}
	defer rows.Close()

	var events []model.Event
	for rows.Next() {
		var evt model.Event
		if err := rows.Scan(
			&evt.ID, &evt.SessionID, &evt.Seq, &evt.Type, &evt.Data, &evt.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		events = append(events, evt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating events: %w", err)
	}
	return events, nil
}

// StreamEvents returns a channel that receives new events for the session
// starting from afterSeq. The channel is closed when the context is cancelled.
// Callers must consume from the channel to prevent blocking.
func (s *Store) StreamEvents(ctx context.Context, sessionID string, afterSeq int64) (<-chan model.Event, error) {
	// First, fetch any existing events after afterSeq.
	existing, err := s.GetEvents(ctx, sessionID, afterSeq, 1000)
	if err != nil {
		return nil, fmt.Errorf("fetching existing events: %w", err)
	}

	ch := make(chan model.Event, 64)

	// Register for future events.
	s.mu.Lock()
	s.watchers[sessionID] = append(s.watchers[sessionID], ch)
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			watchers := s.watchers[sessionID]
			for i, w := range watchers {
				if w == ch {
					s.watchers[sessionID] = append(watchers[:i], watchers[i+1:]...)
					break
				}
			}
			if len(s.watchers[sessionID]) == 0 {
				delete(s.watchers, sessionID)
			}
			s.mu.Unlock()
			close(ch)
		}()

		// Send existing events first.
		for _, evt := range existing {
			select {
			case ch <- evt:
			case <-ctx.Done():
				return
			}
		}

		// Wait for context cancellation; future events arrive via notifyWatchers.
		<-ctx.Done()
	}()

	return ch, nil
}

// notifyWatchers sends an event to all registered watchers for the event's session.
func (s *Store) notifyWatchers(evt model.Event) {
	s.mu.RLock()
	watchers := s.watchers[evt.SessionID]
	s.mu.RUnlock()

	for _, ch := range watchers {
		select {
		case ch <- evt:
		default:
			// Drop if the watcher is too slow — they can catch up via GetEvents.
		}
	}
}
