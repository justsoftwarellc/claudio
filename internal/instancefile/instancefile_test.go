// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package instancefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	ids, path, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on a directory with no %s: %v", FileName, err)
	}
	if path != "" {
		t.Errorf("path = %q, want empty when no file was found", path)
	}
	if len(ids) != 0 {
		t.Errorf("ids = %v, want none", ids)
	}
}

func TestAppendCreatesThenAppends(t *testing.T) {
	dir := t.TempDir()

	if err := Append(dir, "a3f9c2"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := Append(dir, "b7d1e4"); err != nil {
		t.Fatalf("Append (second): %v", err)
	}

	ids, path, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := filepath.Join(dir, FileName); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if len(ids) != 2 || ids[0] != "a3f9c2" || ids[1] != "b7d1e4" {
		t.Errorf("ids = %v, want [a3f9c2 b7d1e4] in creation order", ids)
	}
}

// The file is a real YAML document with an `instances:` key, not a bare
// id list — a user opening it should see a schema they can extend, and
// the strict decoder below depends on the key being there.
func TestAppendWritesYAMLUnderInstancesKey(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, "a3f9c2"); err != nil {
		t.Fatalf("Append: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "instances:") {
		t.Errorf("file has no `instances:` key:\n%s", got)
	}
	if !strings.Contains(got, "- a3f9c2") {
		t.Errorf("file does not list the id as a sequence entry:\n%s", got)
	}
}

func TestAppendIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := Append(dir, "a3f9c2"); err != nil {
			t.Fatalf("Append #%d: %v", i, err)
		}
	}

	ids, _, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ids) != 1 {
		t.Errorf("ids = %v, want the id recorded exactly once", ids)
	}
}

func TestRemoveDropsOnlyTheNamedID(t *testing.T) {
	dir := t.TempDir()
	mustAppend(t, dir, "a3f9c2", "b7d1e4", "c1a2b3")

	if err := Remove(dir, "b7d1e4"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	ids, _, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ids) != 2 || ids[0] != "a3f9c2" || ids[1] != "c1a2b3" {
		t.Errorf("ids = %v, want [a3f9c2 c1a2b3]", ids)
	}
}

// Removing the last id deletes the file rather than leaving an empty
// `instances: []` behind: an empty file would make Find keep resolving
// to this directory and stop the walk at a directory that no longer has
// any instance, shadowing a parent that might.
func TestRemoveLastIDDeletesTheFile(t *testing.T) {
	dir := t.TempDir()
	mustAppend(t, dir, "a3f9c2")

	if err := Remove(dir, "a3f9c2"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Errorf("file still exists after removing the last id (stat err = %v)", err)
	}
}

func TestRemoveUnknownIDIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	mustAppend(t, dir, "a3f9c2")

	if err := Remove(dir, "nothere"); err != nil {
		t.Fatalf("Remove of an id not in the file: %v", err)
	}

	ids, _, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ids) != 1 || ids[0] != "a3f9c2" {
		t.Errorf("ids = %v, want the existing id untouched", ids)
	}
}

func TestRemoveOnMissingFileIsNotAnError(t *testing.T) {
	if err := Remove(t.TempDir(), "a3f9c2"); err != nil {
		t.Fatalf("Remove with no file present: %v", err)
	}
}

// Walking up is what makes a bare `claudio attach` work from any
// subdirectory of the project, not just its root.
func TestFindWalksUpFromSubdirectory(t *testing.T) {
	root := t.TempDir()
	mustAppend(t, root, "a3f9c2")
	deep := filepath.Join(root, "src", "internal", "pkg")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	dir, ids, err := Find(deep)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if dir != root {
		t.Errorf("dir = %q, want the ancestor holding the file (%q)", dir, root)
	}
	if len(ids) != 1 || ids[0] != "a3f9c2" {
		t.Errorf("ids = %v, want [a3f9c2]", ids)
	}
}

// The nearest file wins: a nested project inside a parent project is its
// own instance, not the parent's.
func TestFindStopsAtNearestFile(t *testing.T) {
	root := t.TempDir()
	mustAppend(t, root, "parent1")
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, nested, "child1")

	dir, ids, err := Find(nested)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if dir != nested {
		t.Errorf("dir = %q, want the nearest directory %q", dir, nested)
	}
	if len(ids) != 1 || ids[0] != "child1" {
		t.Errorf("ids = %v, want the nested file's ids", ids)
	}
}

func TestFindReturnsEmptyWhenNoFileAnywhere(t *testing.T) {
	dir, ids, err := Find(t.TempDir())
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if dir != "" || len(ids) != 0 {
		t.Errorf("Find = (%q, %v), want empty when nothing is found", dir, ids)
	}
}

// An unknown key is a decode error, matching internal/config's stance:
// a typo should say so rather than silently doing nothing.
func TestLoadRejectsUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "instances:\n  - a3f9c2\ninstance:\n  - typo\n")

	if _, _, err := Load(dir); err == nil {
		t.Fatal("Load accepted an unknown key, want a decode error")
	}
}

func TestLoadRejectsMalformedYAML(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "instances: [unclosed\n")

	if _, _, err := Load(dir); err == nil {
		t.Fatal("Load accepted malformed YAML, want an error")
	}
}

// A file a user has emptied out, or one left behind by an editor, is
// treated as "no instances here" rather than an error — there is nothing
// ambiguous about it.
func TestLoadEmptyFileYieldsNoIDs(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "")

	ids, path, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on an empty file: %v", err)
	}
	if path == "" {
		t.Error("path is empty, want the found file's path")
	}
	if len(ids) != 0 {
		t.Errorf("ids = %v, want none", ids)
	}
}

func TestLoadSkipsBlankEntries(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "instances:\n  - a3f9c2\n  - \"\"\n  - \"  \"\n  - b7d1e4\n")

	ids, _, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ids) != 2 || ids[0] != "a3f9c2" || ids[1] != "b7d1e4" {
		t.Errorf("ids = %v, want blank entries skipped", ids)
	}
}

// The id is meaningless on another machine, so the file must never be
// committed. It hides itself rather than editing a .gitignore the user
// owns and may have committed.
func TestAppendWritesSelfIgnoringGitExclude(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, "a3f9c2"); err != nil {
		t.Fatalf("Append: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(data), FileName) {
		t.Errorf(".gitignore does not mention %s:\n%s", FileName, data)
	}
}

// Appending to a .gitignore the user already has must preserve what is
// there — this is a file they own and may have committed.
func TestAppendPreservesExistingGitignore(t *testing.T) {
	dir := t.TempDir()
	existing := "node_modules/\ndist/\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Append(dir, "a3f9c2"); err != nil {
		t.Fatalf("Append: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "node_modules/") || !strings.Contains(got, "dist/") {
		t.Errorf("existing .gitignore entries were lost:\n%s", got)
	}
	if !strings.Contains(got, FileName) {
		t.Errorf(".gitignore does not mention %s:\n%s", FileName, got)
	}
}

// A second create must not write the ignore line twice.
func TestAppendDoesNotDuplicateGitignoreEntry(t *testing.T) {
	dir := t.TempDir()
	mustAppend(t, dir, "a3f9c2", "b7d1e4")

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), FileName); n != 1 {
		t.Errorf("%s appears %d times in .gitignore, want 1:\n%s", FileName, n, data)
	}
}

// A .gitignore whose last line has no trailing newline must not end up
// with the entry glued onto it ("dist/.claudio").
func TestAppendGitignoreWithoutTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("dist/"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Append(dir, "a3f9c2"); err != nil {
		t.Fatalf("Append: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "dist/"+FileName) {
		t.Errorf("entry was glued onto the last line:\n%s", data)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == FileName {
			return
		}
	}
	t.Errorf("%s is not on a line of its own:\n%s", FileName, data)
}

// An entry already covered by the user's own .gitignore (a broader glob,
// say) should not be duplicated by us.
func TestAppendRespectsExistingIgnoreLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(FileName+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Append(dir, "a3f9c2"); err != nil {
		t.Fatalf("Append: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), FileName); n != 1 {
		t.Errorf("%s appears %d times, want the existing line left alone:\n%s", FileName, n, data)
	}
}

// ~/.claudio is Claudio's own state DIRECTORY (global config, repos,
// state.db), and Find walks up through $HOME on the way to the root. A
// directory named .claudio is not a pointer file and must be stepped
// over silently — reading one as a file errors, which would break every
// bare command run anywhere under $HOME.
func TestLoadIgnoresADirectoryNamedClaudio(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, FileName), 0o755); err != nil {
		t.Fatal(err)
	}

	ids, path, err := Load(dir)
	if err != nil {
		t.Fatalf("Load with a .claudio directory present: %v", err)
	}
	if path != "" || len(ids) != 0 {
		t.Errorf("Load = (%q, %v), want it treated as no file at all", path, ids)
	}
}

func TestFindWalksPastADirectoryNamedClaudio(t *testing.T) {
	root := t.TempDir()
	// Mirrors $HOME: a .claudio *directory* here, and a real pointer file
	// in a project below it.
	if err := os.MkdirAll(filepath.Join(root, FileName), 0o755); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "Projects", "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, project, "a3f9c2")

	dir, ids, err := Find(project)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if dir != project {
		t.Errorf("dir = %q, want the project's own file at %q", dir, project)
	}
	if len(ids) != 1 || ids[0] != "a3f9c2" {
		t.Errorf("ids = %v, want [a3f9c2]", ids)
	}
}

// Walking up from a directory whose only ancestor .claudio is a
// directory must report "nothing found", not an error.
func TestFindReturnsNothingWhenOnlyADirectoryNamedClaudioExists(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, FileName), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "Projects", "app")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	dir, ids, err := Find(deep)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if dir != "" || len(ids) != 0 {
		t.Errorf("Find = (%q, %v), want nothing found", dir, ids)
	}
}

func mustAppend(t *testing.T, dir string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := Append(dir, id); err != nil {
			t.Fatalf("Append(%s): %v", id, err)
		}
	}
}

func writeRaw(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
