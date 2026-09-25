// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package imagebuild

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A pnpm workspace with two packages, one depending on the other. The
// second package is what makes this a monorepo test rather than another
// single-package install: `workspace:*` only resolves if pnpm actually
// linked the workspace, which a copied-file install would not.
func writePnpmMonorepo(t *testing.T, dir string) {
	t.Helper()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("pnpm-workspace.yaml", "packages:\n  - 'packages/*'\n")
	write("package.json", `{"name":"fixture-root","private":true,"version":"1.0.0"}`)
	write("packages/lib/package.json", `{"name":"@fixture/lib","version":"1.0.0","main":"index.js"}`)
	write("packages/lib/index.js", "module.exports = () => 'from-lib'\n")
	write("packages/app/package.json",
		`{"name":"@fixture/app","version":"1.0.0","main":"index.js","dependencies":{"@fixture/lib":"workspace:*","is-odd":"3.0.1"}}`)
	write("packages/app/index.js", "module.exports = require('@fixture/lib')\n")
}

// TestPnpmInstallWorksInBaseImage is ROD-140's core requirement and
// ROD-139's real verification: pnpm must actually run and install a
// workspace inside a container built from the base image, with the two
// bind mounts a real instance has (/repo and /home/agent) and as the
// unprivileged agent user.
//
// This exists because the only prior pnpm coverage asserted that a
// generated Dockerfile *contained the string* `npm install -g pnpm` —
// which passes whether or not pnpm can run, and is exactly how the
// failure reached a user. Each assertion below is aimed at one of
// ROD-139's four ranked causes so a regression says which one came back.
func TestPnpmInstallWorksInBaseImage(t *testing.T) {
	dockerAvailable(t)
	baseTag := uniqueTag("claudio-test/base-for-pnpm")
	t.Cleanup(func() { removeImage(t, baseTag) })
	if err := buildBaseAs(context.Background(), baseTag, BuildArgsForHost(os.Getuid(), os.Getgid())); err != nil {
		t.Fatalf("build base: %v", err)
	}

	repoDir := t.TempDir()
	homeDir := t.TempDir()
	writePnpmMonorepo(t, repoDir)

	// Cause 1 (`pnpm: not found`): the base must ship a working pnpm
	// with no per-repo image: declaration at all — that opt-in route
	// additionally requires `claudio image build --repo`, which is what
	// made "just run pnpm install" fail for a repo that declared nothing.
	out, err := exec.Command("docker", "run", "--rm", "--entrypoint", "pnpm", baseTag, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("pnpm not runnable in the base image (ROD-139 cause 1): %v: %s", err, out)
	}

	// Cause 3 (EACCES) and cause 4 (missing chmod) both show up here as
	// a failure to run as the *agent* user against mounted, host-owned
	// directories — the base runs USER agent, uid-matched to this host.
	run := exec.Command("docker", "run", "--rm",
		"--entrypoint", "sh",
		"-v", repoDir+":/repo",
		"-v", homeDir+":/home/agent",
		"-w", "/repo",
		baseTag, "-c", "pnpm install --no-frozen-lockfile 2>&1")
	out, err = run.CombinedOutput()
	if err != nil {
		t.Fatalf("pnpm install failed in container: %v\n%s", err, out)
	}
	if bytes.Contains(out, []byte("EXDEV")) {
		t.Errorf("pnpm hit EXDEV across the bind mounts (ROD-139 cause 2):\n%s", out)
	}
	if bytes.Contains(out, []byte("EACCES")) || bytes.Contains(out, []byte("permission denied")) {
		t.Errorf("pnpm hit a permission error (ROD-139 causes 3/4):\n%s", out)
	}

	// The install having *reported* success is not the same as a usable
	// node_modules — resolve both a registry dependency and the
	// workspace link from inside the container.
	resolve := exec.Command("docker", "run", "--rm",
		"--entrypoint", "node",
		"-v", repoDir+":/repo",
		"-v", homeDir+":/home/agent",
		"-w", "/repo/packages/app",
		baseTag, "-e", `console.log(require('is-odd')(3), require('@fixture/lib')())`)
	out, err = resolve.CombinedOutput()
	if err != nil {
		t.Fatalf("dependencies not resolvable after pnpm install: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("true from-lib")) {
		t.Errorf("resolution output = %q, want registry dep and workspace link both resolving", out)
	}
}

// A repo pinning packageManager is the ordinary pnpm setup, and it makes
// corepack *write* to its cache. Verified because priming that cache at
// build time is only correct if it stays writable: a root-owned cache
// turns this very common case into a hard "Failed to create cache
// directory" failure — found empirically while fixing ROD-139, and the
// reason the image chowns /opt/corepack to the agent user.
func TestPnpmRespectsPackageManagerPin(t *testing.T) {
	dockerAvailable(t)
	baseTag := uniqueTag("claudio-test/base-for-pnpm-pin")
	t.Cleanup(func() { removeImage(t, baseTag) })
	if err := buildBaseAs(context.Background(), baseTag, BuildArgsForHost(os.Getuid(), os.Getgid())); err != nil {
		t.Fatalf("build base: %v", err)
	}

	repoDir := t.TempDir()
	homeDir := t.TempDir()
	const pinned = "10.34.5"
	if err := os.WriteFile(filepath.Join(repoDir, "package.json"),
		[]byte(`{"name":"pinned","private":true,"version":"1.0.0","packageManager":"pnpm@`+pinned+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("docker", "run", "--rm",
		"--entrypoint", "pnpm",
		"-v", repoDir+":/repo",
		"-v", homeDir+":/home/agent",
		"-w", "/repo",
		baseTag, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("pnpm with a packageManager pin failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), pinned) {
		t.Errorf("pnpm --version = %q, want the pinned %s honoured", out, pinned)
	}
}
