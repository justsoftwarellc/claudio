package store

import (
	"context"
	"fmt"
	"time"
)

// EventKind names the class of thing an event row records. The events
// table is the append-only log behind docs/architecture.md §5's "what
// lets `claudio status` explain *why* something failed rather than only
// that it did" — so kinds identify the situation, and the free-text
// message carries the particulars (an underlying error string, a port
// number) rather than being encoded into ever-finer kinds.
type EventKind string

const (
	// EventReconciled records a correction the lazy reconciler made to
	// stored intent (docs/architecture.md §10.1) — e.g. an instance whose
	// container vanished being marked stopped. Distinct from a user-driven
	// stop: nobody asked for this, Claudio noticed it.
	EventReconciled EventKind = "reconciled"
)

// Event is a row in the events table. InstanceID is nullable in the
// schema (an event can outlive nothing, since rows cascade away with
// their instance) but every writer so far attributes to an instance.
type Event struct {
	ID         int64
	InstanceID *string
	Kind       EventKind
	Message    string
	CreatedAt  int64
}

// RecordEvent appends one row to the event log. Callers on read paths
// (ls, status) treat a failure here as non-fatal — a lost log line must
// never turn a working listing into an error.
func (s *Store) RecordEvent(ctx context.Context, instanceID string, kind EventKind, message string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO events (instance_id, kind, message, created_at) VALUES (?, ?, ?, ?)`,
		instanceID, kind, message, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("store: record %s event for %s: %w", kind, instanceID, err)
	}
	return nil
}

// ListEvents returns an instance's events oldest-first, the order
// idx_events_instance is built for.
func (s *Store) ListEvents(ctx context.Context, instanceID string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, instance_id, kind, message, created_at
		 FROM events WHERE instance_id = ? ORDER BY created_at, id`, instanceID)
	if err != nil {
		return nil, fmt.Errorf("store: list events for %s: %w", instanceID, err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.InstanceID, &e.Kind, &e.Message, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
