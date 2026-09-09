package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/instancefile"
	"github.com/rodrigomorales/claudio/internal/store"
)

// liveInstances builds a lookup over a fixed set of known ids, standing
// in for the store without needing one.
func liveInstances(known ...string) instanceLookup {
	return func(id string) (store.Instance, error) {
		for _, k := range known {
			if k == id {
				branch := "feat/" + k
				return store.Instance{ID: k, Branch: branch, DesiredState: store.StateRunning}, nil
			}
		}
		return store.Instance{}, os.ErrNotExist
	}
}

func TestResolveExplicitIDWins(t *testing.T) {
	dir := tempDirWithInstances(t, "a3f9c2", "b7d1e4")

	id, warn, err := resolveInstanceID([]string{"explicit"}, dir, liveInstances("a3f9c2", "b7d1e4", "explicit"))
	if err != nil {
		t.Fatalf("resolveInstanceID: %v", err)
	}
	if id != "explicit" {
		t.Errorf("id = %q, want the explicit argument to win over the file", id)
	}
	if warn != "" {
		t.Errorf("warn = %q, want none when an id was given explicitly", warn)
	}
}

// An explicit id must not even consult the file, so a malformed or
// ambiguous file never blocks a command the user fully specified.
func TestResolveExplicitIDIgnoresMalformedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, instancefile.FileName), []byte("instances: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	id, _, err := resolveInstanceID([]string{"explicit"}, dir, liveInstances("explicit"))
	if err != nil {
		t.Fatalf("resolveInstanceID: %v", err)
	}
	if id != "explicit" {
		t.Errorf("id = %q, want %q", id, "explicit")
	}
}

func TestResolveSingleEntryIsUsedImplicitly(t *testing.T) {
	dir := tempDirWithInstances(t, "a3f9c2")

	id, warn, err := resolveInstanceID(nil, dir, liveInstances("a3f9c2"))
	if err != nil {
		t.Fatalf("resolveInstanceID: %v", err)
	}
	if id != "a3f9c2" {
		t.Errorf("id = %q, want the sole tied instance", id)
	}
	if warn != "" {
		t.Errorf("warn = %q, want none", warn)
	}
}

func TestResolveWorksFromSubdirectory(t *testing.T) {
	root := tempDirWithInstances(t, "a3f9c2")
	deep := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	id, _, err := resolveInstanceID(nil, deep, liveInstances("a3f9c2"))
	if err != nil {
		t.Fatalf("resolveInstanceID: %v", err)
	}
	if id != "a3f9c2" {
		t.Errorf("id = %q, want the ancestor file's instance", id)
	}
}

// Multiple live instances is the case the user must disambiguate — the
// error has to name them, with enough detail (branch) to tell them apart.
func TestResolveMultipleEntriesRefusesAndListsThem(t *testing.T) {
	dir := tempDirWithInstances(t, "a3f9c2", "b7d1e4")

	_, _, err := resolveInstanceID(nil, dir, liveInstances("a3f9c2", "b7d1e4"))
	if err == nil {
		t.Fatal("resolveInstanceID succeeded with two tied instances, want an error")
	}
	msg := err.Error()
	for _, want := range []string{"a3f9c2", "b7d1e4", "feat/a3f9c2", "feat/b7d1e4"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not mention %q:\n%s", want, msg)
		}
	}
}

// A stale id is warned about and skipped, not silently dropped: skipping
// keeps the command working, the warning keeps the drift visible.
func TestResolveStaleIDIsWarnedAndSkipped(t *testing.T) {
	dir := tempDirWithInstances(t, "gone123", "a3f9c2")

	id, warn, err := resolveInstanceID(nil, dir, liveInstances("a3f9c2"))
	if err != nil {
		t.Fatalf("resolveInstanceID: %v", err)
	}
	if id != "a3f9c2" {
		t.Errorf("id = %q, want the one live instance", id)
	}
	if !strings.Contains(warn, "gone123") {
		t.Errorf("warn = %q, want it to name the stale id", warn)
	}
}

// The file is left alone when an id goes stale — Remove is destroy's job,
// and a transiently unreadable store must not silently rewrite the file.
func TestResolveDoesNotRewriteFileOnStaleID(t *testing.T) {
	dir := tempDirWithInstances(t, "gone123", "a3f9c2")

	if _, _, err := resolveInstanceID(nil, dir, liveInstances("a3f9c2")); err != nil {
		t.Fatalf("resolveInstanceID: %v", err)
	}

	ids, _, err := instancefile.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Errorf("file now lists %v, want both ids left in place", ids)
	}
}

func TestResolveAllEntriesStaleErrors(t *testing.T) {
	dir := tempDirWithInstances(t, "gone1", "gone2")

	_, _, err := resolveInstanceID(nil, dir, liveInstances())
	if err == nil {
		t.Fatal("resolveInstanceID succeeded with every id stale, want an error")
	}
	if !strings.Contains(err.Error(), "gone1") || !strings.Contains(err.Error(), "gone2") {
		t.Errorf("error should name the stale ids:\n%s", err)
	}
}

// Outside any Claudio directory the error must name both ways out, not
// just reprint a usage line.
func TestResolveNoFileNamesBothWaysOut(t *testing.T) {
	_, _, err := resolveInstanceID(nil, t.TempDir(), liveInstances("a3f9c2"))
	if err == nil {
		t.Fatal("resolveInstanceID succeeded with no file present, want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "claudio create .") {
		t.Errorf("error does not mention how the file gets created:\n%s", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "id") {
		t.Errorf("error does not mention passing an id:\n%s", msg)
	}
}

func TestResolveMalformedFileReportsIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, instancefile.FileName), []byte("instances: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := resolveInstanceID(nil, dir, liveInstances()); err == nil {
		t.Fatal("resolveInstanceID accepted a malformed file, want an error")
	}
}

// More than one positional argument is a usage mistake, not an id.
func TestResolveTooManyArgsErrors(t *testing.T) {
	if _, _, err := resolveInstanceID([]string{"a", "b"}, t.TempDir(), liveInstances("a", "b")); err == nil {
		t.Fatal("resolveInstanceID accepted two positional ids, want an error")
	}
}

func tempDirWithInstances(t *testing.T, ids ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, id := range ids {
		if err := instancefile.Append(dir, id); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	return dir
}
