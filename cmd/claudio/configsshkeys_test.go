// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/config"
)

func writeTestKey(t *testing.T, name string) string {
	t.Helper()
	// Symlinks resolved so the path compares equal to what gets stored
	// (macOS's /var is a link to /private/var).
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The round trip a user actually performs, through the `config`
// namespace so the dispatch is covered too: add a key, list it, remove
// it, list nothing.
func TestConfigAddGetRemoveSSHKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDIO_HOME", home)
	key := writeTestKey(t, "id_ed25519")
	ctx := context.Background()

	if code := cmdConfig(ctx, []string{"add", "ssh_keys", key}); code != 0 {
		t.Fatalf("config add ssh_keys = %d, want 0", code)
	}
	cfg, err := config.LoadGlobalConfig(filepath.Join(home, "config.yml"))
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	if len(cfg.SSHKeys) != 1 || cfg.SSHKeys[0] != key {
		t.Fatalf("ssh_keys = %v, want [%s]", cfg.SSHKeys, key)
	}

	var code int
	out := captureStdout(t, func() { code = cmdConfig(ctx, []string{"get", "ssh_keys"}) })
	// Bare paths on stdout, one per line, so the output is scriptable.
	if code != 0 || out != key+"\n" {
		t.Fatalf("config get ssh_keys = %d, stdout %q; want 0 and the key alone", code, out)
	}

	if code := cmdConfig(ctx, []string{"remove", "ssh_keys", key}); code != 0 {
		t.Fatalf("config remove ssh_keys = %d, want 0", code)
	}
	out = captureStdout(t, func() { code = cmdConfig(ctx, []string{"get", "ssh_keys"}) })
	if code != 0 || out != "" {
		t.Fatalf("config get ssh_keys after remove = %d, stdout %q; want 0 and empty stdout", code, out)
	}
}

// A relative path is stored absolute: the config is read from whatever
// directory a later command runs in, where "./id" means something else.
func TestConfigAddSSHKeyStoresRelativePathAbsolute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDIO_HOME", home)
	key := writeTestKey(t, "id_ed25519")
	t.Chdir(filepath.Dir(key))

	if code := cmdConfigAdd(context.Background(), []string{"ssh_keys", "id_ed25519"}); code != 0 {
		t.Fatalf("config add ssh_keys id_ed25519 = %d, want 0", code)
	}
	cfg, err := config.LoadGlobalConfig(filepath.Join(home, "config.yml"))
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	if len(cfg.SSHKeys) != 1 || cfg.SSHKeys[0] != key {
		t.Fatalf("ssh_keys = %v, want the absolute [%s]", cfg.SSHKeys, key)
	}
	// And the same relative spelling removes it again.
	if code := cmdConfigRemove(context.Background(), []string{"ssh_keys", "id_ed25519"}); code != 0 {
		t.Fatalf("config remove ssh_keys id_ed25519 = %d, want 0", code)
	}
}

// Validating at add time is the reason to use the command over editing
// the YAML: each of these must fail here and write nothing.
func TestConfigAddSSHKeyRejectsBadPathsWithoutWriting(t *testing.T) {
	key := writeTestKey(t, "id_ed25519")
	pub := key + ".pub"
	if err := os.WriteFile(pub, []byte("ssh-ed25519 AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"missing file":  {"ssh_keys", filepath.Join(filepath.Dir(key), "gone")},
		"a directory":   {"ssh_keys", filepath.Dir(key)},
		"a public key":  {"ssh_keys", pub},
		"no path":       {"ssh_keys"},
		"two paths":     {"ssh_keys", key, key},
		"unknown key":   {"ssh_kesy", key},
		"no key at all": {},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CLAUDIO_HOME", home)
			if code := cmdConfigAdd(context.Background(), args); code == 0 {
				t.Errorf("cmdConfigAdd(%v) = 0, want nonzero", args)
			}
			if _, err := os.Stat(filepath.Join(home, "config.yml")); !os.IsNotExist(err) {
				t.Error("a rejected add must not create or write config.yml")
			}
		})
	}
}

func TestConfigAddSSHKeyTwiceIsNotAnError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDIO_HOME", home)
	key := writeTestKey(t, "id_ed25519")

	for i := range 2 {
		if code := cmdConfigAdd(context.Background(), []string{"ssh_keys", key}); code != 0 {
			t.Fatalf("add #%d = %d, want 0", i+1, code)
		}
	}
	cfg, err := config.LoadGlobalConfig(filepath.Join(home, "config.yml"))
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	if len(cfg.SSHKeys) != 1 {
		t.Fatalf("ssh_keys = %v, want the key listed once", cfg.SSHKeys)
	}
}

// Removing something that is not listed reports it, rather than
// claiming a removal that did not happen.
func TestConfigRemoveSSHKeyNotListedFails(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())
	if code := cmdConfigRemove(context.Background(), []string{"ssh_keys", "/keys/never-added"}); code == 0 {
		t.Error("cmdConfigRemove of an unlisted key = 0, want nonzero")
	}
}

// `config set ssh_keys` is the obvious first guess after `set editor`;
// it must refuse rather than be read as an unknown key or, worse, write.
func TestConfigSetSSHKeysPointsAtAdd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDIO_HOME", home)
	key := writeTestKey(t, "id_ed25519")

	code, stderr := captureStderrCode(t, func() int { return cmdConfigSet(context.Background(), []string{"ssh_keys", key}) })
	if code == 0 {
		t.Fatal("cmdConfigSet ssh_keys = 0, want nonzero")
	}
	if !strings.Contains(stderr, "config add ssh_keys") {
		t.Errorf("stderr = %q, want it to name `config add ssh_keys`", stderr)
	}
	if _, err := os.Stat(filepath.Join(home, "config.yml")); !os.IsNotExist(err) {
		t.Error("a refused set must not write config.yml")
	}
}
