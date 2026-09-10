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

// The round trip a user actually performs: set an editor, then read it
// back. `sh` stands in for a real editor because it is the one binary
// guaranteed to be on PATH wherever this test runs — the command only
// cares that the name resolves.
func TestConfigSetThenGetEditor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDIO_HOME", home)

	if code := cmdConfigSet(context.Background(), []string{"editor", "sh"}); code != 0 {
		t.Fatalf("cmdConfigSet = %d, want 0", code)
	}

	cfg, err := config.LoadGlobalConfig(filepath.Join(home, "config.yml"))
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	if cfg.Editor != "sh" {
		t.Fatalf("editor = %q, want sh", cfg.Editor)
	}

	var code int
	out := captureStdout(t, func() {
		code = cmdConfigGet(context.Background(), []string{"editor"})
	})
	if code != 0 {
		t.Fatalf("cmdConfigGet = %d, want 0", code)
	}
	// Bare value on stdout, so `$(claudio config get editor)` works.
	if strings.TrimSpace(out) != "sh" {
		t.Fatalf("stdout = %q, want just the editor value", out)
	}
}

// Validating at set time is the whole reason this command exists rather
// than telling people to edit the YAML: a typo must fail here, not at
// the next `claudio open`, and must not be written to the file.
func TestConfigSetEditorRejectsBinaryNotOnPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDIO_HOME", home)

	if code := cmdConfigSet(context.Background(), []string{"editor", "definitely-not-a-real-binary-xyz"}); code == 0 {
		t.Fatal("cmdConfigSet with an unresolvable binary = 0, want nonzero")
	}
	if _, err := os.Stat(filepath.Join(home, "config.yml")); !os.IsNotExist(err) {
		t.Error("a rejected editor must not create or write config.yml")
	}
}

// `claudio config set editor code -n` is a reasonable thing to try given
// the flag-taking editors people use, so the refusal has to explain that
// arguments aren't supported rather than just counting arguments.
func TestConfigSetEditorRejectsArguments(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	code, stderr := captureStderrCode(t, func() int {
		return cmdConfigSet(context.Background(), []string{"editor", "sh", "-n"})
	})
	if code == 0 {
		t.Fatal("cmdConfigSet with arguments = 0, want nonzero")
	}
	if !strings.Contains(stderr, "arguments are not supported") {
		t.Errorf("stderr should explain that arguments are unsupported, got: %q", stderr)
	}
}

// An unset editor is not an error state for the config itself, but `get`
// has nothing to print — and printing an empty line to stdout would make
// `$(claudio config get editor)` silently yield "".
func TestConfigGetEditorUnsetFailsWithEmptyStdout(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	var code int
	out := captureStdout(t, func() {
		code = cmdConfigGet(context.Background(), []string{"editor"})
	})
	if code == 0 {
		t.Fatal("cmdConfigGet with no editor set = 0, want nonzero")
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("stdout must stay empty when nothing is configured, got %q", out)
	}
}

// Unknown keys must be named, not silently accepted — `config set edtior`
// otherwise writes nothing and reports success.
func TestConfigSetAndGetRejectUnknownKeys(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	if code := cmdConfigSet(context.Background(), []string{"edtior", "sh"}); code == 0 {
		t.Error("cmdConfigSet with an unknown key = 0, want nonzero")
	}
	if code := cmdConfigGet(context.Background(), []string{"edtior"}); code == 0 {
		t.Error("cmdConfigGet with an unknown key = 0, want nonzero")
	}
}

// The namespace must route the new subcommands; a set/get that only
// works when called directly is not reachable by any user.
func TestConfigDispatchesSetAndGet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDIO_HOME", home)

	if code := cmdConfig(context.Background(), []string{"set", "editor", "sh"}); code != 0 {
		t.Fatalf("cmdConfig(set editor sh) = %d, want 0", code)
	}
	var code int
	out := captureStdout(t, func() {
		code = cmdConfig(context.Background(), []string{"get", "editor"})
	})
	if code != 0 || strings.TrimSpace(out) != "sh" {
		t.Fatalf("cmdConfig(get editor) = %d, stdout %q; want 0 and \"sh\"", code, out)
	}
}
