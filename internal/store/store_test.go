package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(context.Background(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s1, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	s1.Close()

	s2, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("second open (re-migrate): %v", err)
	}
	defer s2.Close()
}

func insertInstance(t *testing.T, s *Store, id string) {
	t.Helper()
	_, err := s.db.Exec(
		`INSERT INTO instances (id, repo_url, repo_root, worktree_dir, branch, image, runtime_profile, created_at, last_active)
		 VALUES (?, 'git@github.com:acme/web.git', '/repos/acme', '/repos/acme/worktrees/'||?, 'main', 'claudio/base', 'orbstack', 0, 0)`,
		id, id)
	if err != nil {
		t.Fatalf("insertInstance: %v", err)
	}
}

func TestAllocatePortAssignsFreePortAndProbes(t *testing.T) {
	s := openTest(t)
	insertInstance(t, s, "inst-1")

	port, err := s.AllocatePort(context.Background(), "inst-1", 3000, "web", PortDetected, nil, 43000, 43010)
	if err != nil {
		t.Fatalf("AllocatePort: %v", err)
	}
	if port < 43000 || port > 43010 {
		t.Fatalf("port %d out of requested range", port)
	}

	mappings, err := s.PortMappings(context.Background(), "inst-1")
	if err != nil {
		t.Fatalf("PortMappings: %v", err)
	}
	if len(mappings) != 1 || mappings[0].HostPort != port {
		t.Fatalf("expected one mapping with host port %d, got %+v", port, mappings)
	}
}

func TestAllocatePortConcurrentNeverCollides(t *testing.T) {
	s := openTest(t)

	const n = 8
	for i := 0; i < n; i++ {
		insertInstance(t, s, idFor(i))
	}

	var wg sync.WaitGroup
	ports := make([]int, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ports[i], errs[i] = s.AllocatePort(context.Background(), idFor(i), 3000, "web", PortDetected, nil, 43000, 43007)
		}(i)
	}
	wg.Wait()

	seen := make(map[int]bool)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("instance %d: AllocatePort: %v", i, err)
		}
		if seen[ports[i]] {
			t.Fatalf("port %d allocated twice", ports[i])
		}
		seen[ports[i]] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d distinct ports, got %d", n, len(seen))
	}
}

func TestAllocatePortRangeExhausted(t *testing.T) {
	s := openTest(t)
	insertInstance(t, s, "a")
	insertInstance(t, s, "b")

	if _, err := s.AllocatePort(context.Background(), "a", 3000, "web", PortDetected, nil, 43000, 43000); err != nil {
		t.Fatalf("first allocation: %v", err)
	}
	_, err := s.AllocatePort(context.Background(), "b", 3000, "web", PortDetected, nil, 43000, 43000)
	if err != ErrPortRangeExhausted {
		t.Fatalf("expected ErrPortRangeExhausted, got %v", err)
	}
}

func idFor(i int) string {
	return "inst-" + string(rune('a'+i))
}
