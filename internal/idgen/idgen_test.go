package idgen

import (
	"strings"
	"testing"
)

func TestNewFormat(t *testing.T) {
	id, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	parts := strings.Split(id, "-")
	if len(parts) != 2 {
		t.Fatalf("New() = %q, want exactly one hyphen separating adjective and noun", id)
	}
	if parts[0] == "" || parts[1] == "" {
		t.Fatalf("New() = %q, want non-empty adjective and noun", id)
	}
}

func TestNewVaries(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		id, err := New()
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		seen[id] = true
	}
	if len(seen) < 2 {
		t.Fatalf("New() produced only %d distinct value(s) across 50 calls, want variety", len(seen))
	}
}

func TestNoAmbiguousPrefixes(t *testing.T) {
	checkNoPrefixCollisions(t, adjectives)
	checkNoPrefixCollisions(t, nouns)
}

func checkNoPrefixCollisions(t *testing.T, words []string) {
	t.Helper()
	for i, a := range words {
		for j, b := range words {
			if i == j {
				continue
			}
			if strings.HasPrefix(a, b) || strings.HasPrefix(b, a) {
				t.Errorf("words %q and %q share a prefix — breaks unambiguous-prefix matching (docs/architecture.md §9)", a, b)
			}
		}
	}
}
