package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/core"
	"github.com/rodrigomorales/claudio/internal/instancefile"
	"github.com/rodrigomorales/claudio/internal/store"
)

func TestParseChoice(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		count   int
		want    int
		wantErr bool
	}{
		{"first", "1", 3, 0, false},
		{"last", "3", 3, 2, false},
		{"surrounding space", "  2  ", 3, 1, false},
		{"zero is out of range", "0", 3, 0, true},
		{"past the end", "4", 3, 0, true},
		{"negative", "-1", 3, 0, true},
		{"not a number", "abc", 3, 0, true},
		{"empty", "", 3, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseChoice(tc.input, tc.count)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseChoice(%q, %d) = %d, want an error", tc.input, tc.count, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseChoice(%q, %d): %v", tc.input, tc.count, err)
			}
			if got != tc.want {
				t.Errorf("parseChoice(%q, %d) = %d, want %d", tc.input, tc.count, got, tc.want)
			}
		})
	}
}

// "q" cancels rather than erroring — the user changed their mind, which
// is not a mistake to report back to them.
func TestParseChoiceCancel(t *testing.T) {
	for _, input := range []string{"q", "Q", " q "} {
		got, err := parseChoice(input, 3)
		if err != nil {
			t.Errorf("parseChoice(%q): %v, want a clean cancel", input, err)
		}
		if got != choiceCanceled {
			t.Errorf("parseChoice(%q) = %d, want choiceCanceled", input, got)
		}
	}
}

// A link whose instance clearly belongs to a different repo is worth
// flagging — but only flagging: a link is cheap to undo, and the user
// may well know something the heuristic doesn't.
func TestRepoMatchesDirectory(t *testing.T) {
	cases := []struct {
		name    string
		repoURL string
		dir     string
		want    bool
	}{
		{"local source, same dir", "file:///Users/me/Projects/web", "/Users/me/Projects/web", true},
		{"local source, different dir", "file:///Users/me/Projects/web", "/Users/me/Projects/api", false},
		{"ssh remote matching basename", "git@github.com:acme/web.git", "/Users/me/Projects/web", true},
		{"https remote matching basename", "https://github.com/acme/web.git", "/Users/me/Projects/web", true},
		{"ssh remote, unrelated dir", "git@github.com:acme/web.git", "/Users/me/Projects/unrelated", false},
		{"greenfield instance", "local:market-research", "/Users/me/Projects/market-research", true},
		{"empty repo url", "", "/Users/me/Projects/web", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := repoMatchesDirectory(tc.repoURL, tc.dir); got != tc.want {
				t.Errorf("repoMatchesDirectory(%q, %q) = %v, want %v", tc.repoURL, tc.dir, got, tc.want)
			}
		})
	}
}

func TestLinkWritesFileAndIgnoresIt(t *testing.T) {
	dir := t.TempDir()

	msg, err := linkInstance(dir, store.Instance{ID: "a3f9c2", RepoURL: "file://" + dir})
	if err != nil {
		t.Fatalf("linkInstance: %v", err)
	}
	if !strings.Contains(msg, "a3f9c2") {
		t.Errorf("message %q does not name the linked instance", msg)
	}

	ids, _, err := instancefile.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "a3f9c2" {
		t.Errorf("ids = %v, want [a3f9c2]", ids)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(data), instancefile.FileName) {
		t.Errorf(".gitignore does not cover %s:\n%s", instancefile.FileName, data)
	}
}

// Linking the same instance twice must not duplicate it, and must say so
// rather than reporting a second successful link.
func TestLinkTwiceIsANoOp(t *testing.T) {
	dir := t.TempDir()
	inst := store.Instance{ID: "a3f9c2", RepoURL: "file://" + dir}

	if _, err := linkInstance(dir, inst); err != nil {
		t.Fatalf("first link: %v", err)
	}
	msg, err := linkInstance(dir, inst)
	if err != nil {
		t.Fatalf("second link: %v", err)
	}
	if !strings.Contains(strings.ToLower(msg), "already") {
		t.Errorf("message %q does not say the instance was already linked", msg)
	}

	ids, _, err := instancefile.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Errorf("ids = %v, want the id recorded exactly once", ids)
	}
}

// Linking an instance from an unrelated repo is allowed, but the message
// has to point it out — otherwise a typo'd id silently ties the wrong
// instance to the directory.
func TestLinkWarnsOnRepoMismatch(t *testing.T) {
	dir := t.TempDir()

	msg, err := linkInstance(dir, store.Instance{ID: "a3f9c2", RepoURL: "git@github.com:acme/something-else.git"})
	if err != nil {
		t.Fatalf("linkInstance: %v", err)
	}
	if !strings.Contains(msg, "something-else") {
		t.Errorf("message %q does not mention the mismatched repo", msg)
	}

	ids, _, err := instancefile.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Errorf("the link should still have been made; ids = %v", ids)
	}
}

func TestLinkAppendsToExistingFile(t *testing.T) {
	dir := t.TempDir()
	if err := instancefile.Append(dir, "first1"); err != nil {
		t.Fatal(err)
	}

	if _, err := linkInstance(dir, store.Instance{ID: "second", RepoURL: "file://" + dir}); err != nil {
		t.Fatalf("linkInstance: %v", err)
	}

	ids, _, err := instancefile.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "first1" || ids[1] != "second" {
		t.Errorf("ids = %v, want [first1 second]", ids)
	}
}

func TestUnlinkRemovesOnlyTheNamedID(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"a3f9c2", "b7d1e4"} {
		if err := instancefile.Append(dir, id); err != nil {
			t.Fatal(err)
		}
	}

	msg, err := unlinkInstance(dir, "a3f9c2")
	if err != nil {
		t.Fatalf("unlinkInstance: %v", err)
	}
	if !strings.Contains(msg, "a3f9c2") {
		t.Errorf("message %q does not name the unlinked instance", msg)
	}

	ids, _, err := instancefile.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "b7d1e4" {
		t.Errorf("ids = %v, want [b7d1e4]", ids)
	}
}

// Unlinking the last id removes the file, and the message should say so
// — the directory has stopped being a Claudio directory.
func TestUnlinkLastIDDeletesFileAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	if err := instancefile.Append(dir, "a3f9c2"); err != nil {
		t.Fatal(err)
	}

	msg, err := unlinkInstance(dir, "a3f9c2")
	if err != nil {
		t.Fatalf("unlinkInstance: %v", err)
	}
	if !strings.Contains(msg, instancefile.FileName) {
		t.Errorf("message %q does not mention removing %s", msg, instancefile.FileName)
	}
	if _, err := os.Stat(filepath.Join(dir, instancefile.FileName)); !os.IsNotExist(err) {
		t.Errorf("%s still exists after unlinking the last id", instancefile.FileName)
	}
}

func TestUnlinkNotLinkedIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := instancefile.Append(dir, "a3f9c2"); err != nil {
		t.Fatal(err)
	}

	if _, err := unlinkInstance(dir, "nothere"); err == nil {
		t.Fatal("unlinkInstance succeeded for an id that was never linked, want an error")
	}
}

// Formatting the picker is worth pinning: the id is what the user types
// next, so it has to be present and readable for every instance.
func TestFormatInstanceChoices(t *testing.T) {
	views := []core.InstanceView{
		{Instance: store.Instance{ID: "swift-lynx", Branch: "main", RepoURL: "git@github.com:acme/web.git"}},
		{Instance: store.Instance{ID: "jolly-badger", Branch: "feat/auth", RepoURL: "file:///Users/me/sample-app"}},
	}

	out := formatInstanceChoices(views)
	for _, want := range []string{"1)", "2)", "swift-lynx", "jolly-badger", "main", "feat/auth"} {
		if !strings.Contains(out, want) {
			t.Errorf("choices do not contain %q:\n%s", want, out)
		}
	}
}
