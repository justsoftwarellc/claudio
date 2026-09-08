package store

import (
	"context"
	"testing"
)

func recordEvent(t *testing.T, s *Store, instanceID, message string) {
	t.Helper()
	if err := s.RecordEvent(context.Background(), instanceID, EventReconciled, message); err != nil {
		t.Fatalf("RecordEvent(%s, %q): %v", instanceID, message, err)
	}
}

func TestRecordEventAppendsInOrder(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	insertInstance(t, s, "inst-1")

	recordEvent(t, s, "inst-1", "first")
	recordEvent(t, s, "inst-1", "second")

	events, err := s.ListEvents(ctx, "inst-1")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	// Two events written in the same second must still come back in write
	// order — created_at alone cannot order them at this resolution.
	if events[0].Message != "first" || events[1].Message != "second" {
		t.Errorf("messages = [%q %q], want [first second]", events[0].Message, events[1].Message)
	}
	if events[0].Kind != EventReconciled {
		t.Errorf("Kind = %q, want %q", events[0].Kind, EventReconciled)
	}
	if events[0].InstanceID == nil || *events[0].InstanceID != "inst-1" {
		t.Errorf("InstanceID = %v, want inst-1", events[0].InstanceID)
	}
	if events[0].CreatedAt == 0 {
		t.Error("CreatedAt = 0, want a real timestamp")
	}
	if events[0].ID == events[1].ID {
		t.Errorf("both events have ID %d, want distinct autoincrement ids", events[0].ID)
	}
}

func TestListEventsScopesToOneInstance(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	insertInstance(t, s, "inst-1")
	insertInstance(t, s, "inst-2")

	recordEvent(t, s, "inst-1", "mine")
	recordEvent(t, s, "inst-2", "theirs")

	events, err := s.ListEvents(ctx, "inst-1")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Message != "mine" {
		t.Fatalf("events = %+v, want only inst-1's event", events)
	}
}

func TestListEventsForInstanceWithNoEventsIsEmpty(t *testing.T) {
	s := openTest(t)
	insertInstance(t, s, "inst-1")

	events, err := s.ListEvents(context.Background(), "inst-1")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want empty", events)
	}
}

func TestRecordEventRejectsUnknownInstance(t *testing.T) {
	s := openTest(t)

	// foreign_keys(1) is on in the DSN, so an event can never be orphaned
	// by attributing it to an instance that does not exist.
	if err := s.RecordEvent(context.Background(), "ghost", EventReconciled, "orphan"); err == nil {
		t.Fatal("expected a foreign-key error recording an event for an unknown instance")
	}
}

func TestDeleteInstanceCascadesEvents(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	insertInstance(t, s, "inst-1")
	recordEvent(t, s, "inst-1", "doomed")

	if err := s.TransitionDesiredState(ctx, "inst-1", StateDestroyed); err != nil {
		t.Fatalf("TransitionDesiredState: %v", err)
	}
	if err := s.DeleteInstance(ctx, "inst-1"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	events, err := s.ListEvents(ctx, "inst-1")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none after the instance was deleted", events)
	}
}
