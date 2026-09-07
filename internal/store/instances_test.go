package store

import (
	"context"
	"errors"
	"testing"
)

func TestGetInstanceExactID(t *testing.T) {
	s := openTest(t)
	insertInstance(t, s, "brave-otter")

	inst, err := s.GetInstance(context.Background(), "brave-otter")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.ID != "brave-otter" {
		t.Errorf("ID = %q, want brave-otter", inst.ID)
	}
}

func TestGetInstanceUnambiguousPrefix(t *testing.T) {
	s := openTest(t)
	insertInstance(t, s, "brave-otter")
	insertInstance(t, s, "calm-falcon")

	inst, err := s.GetInstance(context.Background(), "brave")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.ID != "brave-otter" {
		t.Errorf("ID = %q, want brave-otter", inst.ID)
	}
}

func TestGetInstanceAmbiguousPrefixErrors(t *testing.T) {
	s := openTest(t)
	insertInstance(t, s, "brave-otter")
	insertInstance(t, s, "brave-falcon")

	_, err := s.GetInstance(context.Background(), "brave")
	if err == nil {
		t.Fatal("expected an error for an ambiguous prefix")
	}
	if !errors.Is(err, ErrAmbiguousID) {
		t.Errorf("error = %v, want ErrAmbiguousID", err)
	}
}

func TestGetInstanceExactMatchWinsOverAmbiguousPrefix(t *testing.T) {
	// "brave-otter" is both an exact ID and a prefix of "brave-otter-2" —
	// the exact match must win outright rather than erroring as
	// ambiguous, since it is unambiguous by definition (see GetInstance's
	// doc).
	s := openTest(t)
	insertInstance(t, s, "brave-otter")
	insertInstance(t, s, "brave-otter-2")

	inst, err := s.GetInstance(context.Background(), "brave-otter")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.ID != "brave-otter" {
		t.Errorf("ID = %q, want brave-otter (exact match)", inst.ID)
	}
}

func TestGetInstanceEmptyStringErrors(t *testing.T) {
	s := openTest(t)
	insertInstance(t, s, "brave-otter")

	_, err := s.GetInstance(context.Background(), "")
	if err == nil {
		t.Fatal("expected an error for an empty idOrName, not a match against every row's prefix")
	}
}

func TestGetInstanceNoMatchErrors(t *testing.T) {
	s := openTest(t)
	insertInstance(t, s, "brave-otter")

	_, err := s.GetInstance(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatal("expected an error for a nonexistent instance")
	}
}
