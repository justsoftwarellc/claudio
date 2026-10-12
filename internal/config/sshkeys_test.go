// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolvedTempDir is t.TempDir() with symlinks resolved: macOS's /var is
// a link to /private/var, and ResolveSSHKeys returns resolved paths.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeKey(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveSSHKeysNoneConfiguredMountsNothing(t *testing.T) {
	got, err := ResolveSSHKeys(nil)
	if err != nil || len(got) != 0 {
		t.Errorf("ResolveSSHKeys(nil) = %v, %v, want no keys and no error", got, err)
	}
}

func TestResolveSSHKeysExpandsHome(t *testing.T) {
	home := resolvedTempDir(t)
	t.Setenv("HOME", home)
	want := writeKey(t, filepath.Join(home, ".ssh", "id_ed25519"))

	got, err := ResolveSSHKeys([]string{"~/.ssh/id_ed25519"})
	if err != nil {
		t.Fatalf("ResolveSSHKeys: %v", err)
	}
	if len(got) != 1 || got[0] != want {
		t.Errorf("ResolveSSHKeys() = %v, want [%s]", got, want)
	}
}

// Docker binds the path it is given, so a key that is a symlink
// (dotfiles managers do this) has to be resolved to its target.
func TestResolveSSHKeysResolvesSymlink(t *testing.T) {
	dir := resolvedTempDir(t)
	real := writeKey(t, filepath.Join(dir, "dotfiles", "id_ed25519"))
	link := filepath.Join(dir, "id_link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveSSHKeys([]string{link})
	if err != nil {
		t.Fatalf("ResolveSSHKeys: %v", err)
	}
	if len(got) != 1 || got[0] != real {
		t.Errorf("ResolveSSHKeys() = %v, want the link target %s", got, real)
	}
}

// Every way a listed key cannot be mounted is an error naming the
// configured path, not a silent skip that resurfaces as a failed fetch.
func TestResolveSSHKeysRejectsWhatItCannotMount(t *testing.T) {
	dir := resolvedTempDir(t)
	a := writeKey(t, filepath.Join(dir, "a", "id_ed25519"))
	b := writeKey(t, filepath.Join(dir, "b", "id_ed25519"))

	cases := []struct {
		name    string
		paths   []string
		wantErr string
	}{
		{"missing file", []string{filepath.Join(dir, "gone")}, "gone"},
		{"a directory", []string{filepath.Join(dir, "a")}, "is not a file"},
		{"relative path", []string{"id_ed25519"}, "not an absolute path"},
		{"two keys with one file name", []string{a, b}, "both named"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveSSHKeys(tc.paths)
			if err == nil {
				t.Fatalf("ResolveSSHKeys(%v) = %v, want an error", tc.paths, got)
			}
			if !strings.Contains(err.Error(), "ssh_keys") || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to name ssh_keys and contain %q", err, tc.wantErr)
			}
		})
	}
}

// Adding a key must not disturb the rest of the machine's config — the
// same regression SetGlobalEditor's yaml.Node edit guards against.
func TestAddGlobalSSHKeyPreservesOtherKeysAndComments(t *testing.T) {
	key := writeKey(t, filepath.Join(resolvedTempDir(t), "id_ed25519"))
	path := writeGlobal(t, "# my machine\neditor: nvim\nresources:\n  memory: 12g\n")

	added, err := AddGlobalSSHKey(path, key)
	if err != nil || !added {
		t.Fatalf("AddGlobalSSHKey = %v, %v, want true and no error", added, err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "# my machine") {
		t.Errorf("comment was dropped:\n%s", raw)
	}
	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("LoadGlobalConfig after add: %v", err)
	}
	if len(cfg.SSHKeys) != 1 || cfg.SSHKeys[0] != key {
		t.Errorf("ssh_keys = %v, want [%s]", cfg.SSHKeys, key)
	}
	if cfg.Editor != "nvim" || cfg.Resources.Memory == nil || *cfg.Resources.Memory != "12g" {
		t.Errorf("other keys not preserved: editor=%q resources=%+v", cfg.Editor, cfg.Resources)
	}
}

func TestAddGlobalSSHKeyCreatesMissingFileAndAppends(t *testing.T) {
	dir := resolvedTempDir(t)
	first := writeKey(t, filepath.Join(dir, "id_first"))
	second := writeKey(t, filepath.Join(dir, "id_second"))
	path := filepath.Join(t.TempDir(), "nested", "config.yml")

	for _, k := range []string{first, second} {
		if added, err := AddGlobalSSHKey(path, k); err != nil || !added {
			t.Fatalf("AddGlobalSSHKey(%s) = %v, %v", k, added, err)
		}
	}
	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	if len(cfg.SSHKeys) != 2 || cfg.SSHKeys[0] != first || cfg.SSHKeys[1] != second {
		t.Errorf("ssh_keys = %v, want [%s %s] in the order added", cfg.SSHKeys, first, second)
	}
}

// The shapes a hand-written file can already have the key in.
func TestAddGlobalSSHKeyExtendsEmptyAndInlineLists(t *testing.T) {
	key := writeKey(t, filepath.Join(resolvedTempDir(t), "id_ed25519"))
	for _, existing := range []string{"ssh_keys:\n", "ssh_keys: []\n"} {
		path := writeGlobal(t, existing)
		if added, err := AddGlobalSSHKey(path, key); err != nil || !added {
			t.Fatalf("AddGlobalSSHKey onto %q = %v, %v", existing, added, err)
		}
		cfg, err := LoadGlobalConfig(path)
		if err != nil {
			raw, _ := os.ReadFile(path)
			t.Fatalf("LoadGlobalConfig after adding onto %q: %v\n%s", existing, err, raw)
		}
		if len(cfg.SSHKeys) != 1 || cfg.SSHKeys[0] != key {
			t.Errorf("after adding onto %q, ssh_keys = %v, want [%s]", existing, cfg.SSHKeys, key)
		}
	}
}

// Adding twice is not an error and not a second entry — including when
// the existing entry spells the same file with a ~. The file is left
// byte-for-byte alone, formatting included.
func TestAddGlobalSSHKeyAlreadyListedChangesNothing(t *testing.T) {
	home := resolvedTempDir(t)
	t.Setenv("HOME", home)
	key := writeKey(t, filepath.Join(home, ".ssh", "id_ed25519"))
	before := "ssh_keys:   [ '~/.ssh/id_ed25519' ]   # mine\n"
	path := writeGlobal(t, before)

	added, err := AddGlobalSSHKey(path, key)
	if err != nil || added {
		t.Fatalf("AddGlobalSSHKey of a listed key = %v, %v, want false and no error", added, err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != before {
		t.Errorf("a no-op add rewrote the file:\n%s", raw)
	}
}

// A rejected key must not reach the file: it would fail every later
// create/start/restart.
func TestAddGlobalSSHKeyRejectsWhatCannotBeMounted(t *testing.T) {
	dir := resolvedTempDir(t)
	listed := writeKey(t, filepath.Join(dir, "a", "id_ed25519"))
	clash := writeKey(t, filepath.Join(dir, "b", "id_ed25519"))
	before := "ssh_keys:\n  - " + listed + "\n"

	for name, key := range map[string]string{
		"missing file":              filepath.Join(dir, "gone"),
		"a directory":               filepath.Join(dir, "a"),
		"same name as a listed key": clash,
	} {
		path := writeGlobal(t, before)
		if added, err := AddGlobalSSHKey(path, key); err == nil {
			t.Errorf("%s: AddGlobalSSHKey = %v, nil; want an error", name, added)
		}
		if raw, _ := os.ReadFile(path); string(raw) != before {
			t.Errorf("%s: a rejected key changed the file:\n%s", name, raw)
		}
	}
}

func TestRemoveGlobalSSHKeyDropsOnlyThatKey(t *testing.T) {
	path := writeGlobal(t, "# my machine\neditor: nvim\nssh_keys:\n  - /keys/one\n  - /keys/two\n")

	removed, err := RemoveGlobalSSHKey(path, "/keys/one")
	if err != nil || !removed {
		t.Fatalf("RemoveGlobalSSHKey = %v, %v, want true and no error", removed, err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "# my machine") {
		t.Errorf("comment was dropped:\n%s", raw)
	}
	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("LoadGlobalConfig after remove: %v", err)
	}
	if len(cfg.SSHKeys) != 1 || cfg.SSHKeys[0] != "/keys/two" || cfg.Editor != "nvim" {
		t.Errorf("after remove: ssh_keys = %v, editor = %q; want [/keys/two] and nvim", cfg.SSHKeys, cfg.Editor)
	}
}

// Remove is the repair for a list that has gone bad, so it must work on
// a key that no longer exists on disk, and removing the last key must
// leave a file that still loads.
func TestRemoveGlobalSSHKeyWorksForAVanishedKeyAndEmptiesTheList(t *testing.T) {
	path := writeGlobal(t, "ssh_keys:\n  - /keys/deleted-from-disk\n")

	removed, err := RemoveGlobalSSHKey(path, "/keys/deleted-from-disk")
	if err != nil || !removed {
		t.Fatalf("RemoveGlobalSSHKey = %v, %v, want true and no error", removed, err)
	}
	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		raw, _ := os.ReadFile(path)
		t.Fatalf("LoadGlobalConfig after removing the last key: %v\n%s", err, raw)
	}
	if len(cfg.SSHKeys) != 0 {
		t.Errorf("ssh_keys = %v, want none", cfg.SSHKeys)
	}
}

func TestRemoveGlobalSSHKeyNotListedChangesNothing(t *testing.T) {
	before := "ssh_keys:   [ /keys/one ]   # mine\n"
	path := writeGlobal(t, before)

	removed, err := RemoveGlobalSSHKey(path, "/keys/other")
	if err != nil || removed {
		t.Fatalf("RemoveGlobalSSHKey of an unlisted key = %v, %v, want false and no error", removed, err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != before {
		t.Errorf("a no-op remove rewrote the file:\n%s", raw)
	}

	// No config.yml at all is the same answer, and must not create one.
	missing := filepath.Join(t.TempDir(), "config.yml")
	if removed, err := RemoveGlobalSSHKey(missing, "/keys/other"); err != nil || removed {
		t.Fatalf("RemoveGlobalSSHKey with no config = %v, %v, want false and no error", removed, err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("a no-op remove created config.yml")
	}
}
