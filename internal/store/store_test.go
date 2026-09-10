// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package store

import (
	"context"
	"errors"
	"fmt"
	"net"
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

	low, high := freeRange(t, 11)
	port, err := s.AllocatePort(context.Background(), "inst-1", 3000, "web", PortDetected, nil, low, high)
	if err != nil {
		t.Fatalf("AllocatePort: %v", err)
	}
	if port < low || port > high {
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

	low, high := freeRange(t, n)
	var wg sync.WaitGroup
	ports := make([]int, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ports[i], errs[i] = s.AllocatePort(context.Background(), idFor(i), 3000, "web", PortDetected, nil, low, high)
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

	only, _ := freeRange(t, 1)
	if _, err := s.AllocatePort(context.Background(), "a", 3000, "web", PortDetected, nil, only, only); err != nil {
		t.Fatalf("first allocation: %v", err)
	}
	_, err := s.AllocatePort(context.Background(), "b", 3000, "web", PortDetected, nil, only, only)
	if err != ErrPortRangeExhausted {
		t.Fatalf("expected ErrPortRangeExhausted, got %v", err)
	}
}

// A port that is free in the store but held by another process must be
// skipped, not retried forever (ROD-119). The allocator used to delete
// its failed reservation and then re-pick the same lowest store-free
// port on every attempt, burning the whole range on one unbindable port
// and reporting the range exhausted.
func TestAllocatePortSkipsPortHeldByAnotherProcess(t *testing.T) {
	s := openTest(t)
	insertInstance(t, s, "inst-1")

	// Hold the bottom of the range for real, the way an unrelated process
	// on the machine would.
	held, listener := listenOnFreePort(t)
	defer listener.Close()

	port, err := s.AllocatePort(context.Background(), "inst-1", 3000, "web", PortDetected, nil, held, held+5)
	if err != nil {
		t.Fatalf("AllocatePort with the low port held: %v", err)
	}
	if port == held {
		t.Fatalf("allocated the held port %d", port)
	}
	if port < held || port > held+5 {
		t.Fatalf("port %d out of requested range", port)
	}
}

// Several consecutive held ports must all be skipped — the retry has to
// make progress across every one of them, not just the first.
func TestAllocatePortSkipsSeveralHeldPorts(t *testing.T) {
	s := openTest(t)
	insertInstance(t, s, "inst-1")

	first, l1 := listenOnFreePort(t)
	defer l1.Close()
	l2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", first+1))
	if err != nil {
		t.Skipf("could not hold %d: %v", first+1, err)
	}
	defer l2.Close()

	port, err := s.AllocatePort(context.Background(), "inst-1", 3000, "web", PortDetected, nil, first, first+5)
	if err != nil {
		t.Fatalf("AllocatePort with two low ports held: %v", err)
	}
	if port == first || port == first+1 {
		t.Fatalf("allocated a held port: %d", port)
	}
}

// A range whose every port is held by another process is genuinely
// unusable, and must be reported as exhausted rather than looping.
func TestAllocatePortRangeFullyHeldByOtherProcesses(t *testing.T) {
	s := openTest(t)
	insertInstance(t, s, "inst-1")

	held, listener := listenOnFreePort(t)
	defer listener.Close()

	// A one-port range consisting only of the held port.
	_, err := s.AllocatePort(context.Background(), "inst-1", 3000, "web", PortDetected, nil, held, held)
	if !errors.Is(err, ErrPortRangeExhausted) {
		t.Fatalf("err = %v, want ErrPortRangeExhausted", err)
	}

	// The failed probe must not leave a reservation behind.
	mappings, err := s.PortMappings(context.Background(), "inst-1")
	if err != nil {
		t.Fatalf("PortMappings: %v", err)
	}
	if len(mappings) != 0 {
		t.Errorf("mappings = %+v, want none after a fully failed allocation", mappings)
	}
}

// freeRange finds a run of n consecutive bindable ports and returns its
// bounds. Tests must not hardcode 43000+: that is the default range
// Claudio itself allocates from, so a developer with instances running
// has those ports genuinely held and every hardcoded test fails on their
// machine — which is exactly how ROD-119 stayed hidden. The ports are
// released before returning, so this is advisory: it establishes a range
// that was free a moment ago, not a reservation.
func freeRange(t *testing.T, n int) (low, high int) {
	t.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		base, l := listenOnFreePort(t)
		l.Close()

		held := make([]net.Listener, 0, n)
		ok := true
		for i := 0; i < n; i++ {
			lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", base+i))
			if err != nil {
				ok = false
				break
			}
			held = append(held, lis)
		}
		for _, lis := range held {
			lis.Close()
		}
		if ok {
			return base, base + n - 1
		}
	}
	t.Skipf("could not find %d consecutive free ports", n)
	return 0, 0
}

// listenOnFreePort binds a port the OS says is free and returns it still
// held, so a test can depend on that port being unbindable rather than on
// whatever happens to be running on the machine.
func listenOnFreePort(t *testing.T) (int, net.Listener) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return l.Addr().(*net.TCPAddr).Port, l
}

func idFor(i int) string {
	return "inst-" + string(rune('a'+i))
}

// TestAllocatePortRangeExhaustedIsMatchableWithErrorsIs pins the contract
// core.describePortRangeExhausted depends on: the exhausted-range failure
// must be identifiable via errors.Is, not by string matching or by
// pointer equality on the sentinel, so the layer that knows *which* range
// was configured can wrap it with that detail without breaking detection.
func TestAllocatePortRangeExhaustedIsMatchableWithErrorsIs(t *testing.T) {
	s := openTest(t)
	insertInstance(t, s, "a")
	insertInstance(t, s, "b")

	only, _ := freeRange(t, 1)
	if _, err := s.AllocatePort(context.Background(), "a", 3000, "web", PortDetected, nil, only, only); err != nil {
		t.Fatalf("first allocation: %v", err)
	}
	_, err := s.AllocatePort(context.Background(), "b", 3000, "web", PortDetected, nil, only, only)
	if !errors.Is(err, ErrPortRangeExhausted) {
		t.Fatalf("errors.Is(%v, ErrPortRangeExhausted) = false, want true", err)
	}
}
