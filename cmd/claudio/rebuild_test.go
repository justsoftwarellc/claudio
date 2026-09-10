// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestShortImageIDTrimsDigestToDockersOwnWidth(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"sha256-prefixed digest", "sha256:98e847299a9f1b2c3d4e5f60718d6d3b0e4b3b72b90c235820c2c651fbf2588f", "98e847299a9f"},
		{"bare digest", "98e847299a9f1b2c3d4e5f60718d6d3b0e", "98e847299a9f"},
		{"already short", "98e847299a9f", "98e847299a9f"},
		{"shorter than 12 is left alone", "98e847", "98e847"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortImageID(tc.in); got != tc.want {
				t.Errorf("shortImageID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Every failure mode of the image lookup must collapse to "", since it
// only feeds a cosmetic drift line — a rebuild that worked must not
// report failure because `docker inspect` didn't cooperate.
func TestContainerImageIDReturnsEmptyForUnknownContainer(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if got := containerImageID(context.Background(), "", "claudio-definitely-no-such-container"); got != "" {
		t.Errorf("containerImageID(nonexistent) = %q, want %q", got, "")
	}
}

// rebuild shares start/restart's flag parsing, so the contract worth
// pinning here is that it accepts exactly the same forms under its own
// name — including rejecting a stray flag rather than silently treating
// it as an instance ID.
//
// Since ROD-117 the id is optional and this only separates flags from
// positionals: no args is now a valid call (the id comes from the
// .claudio file), and how many positionals are acceptable is
// resolveInstanceID's rule to enforce, not this function's.
func TestRebuildFlagParsing(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantRest  []string
		wantFresh bool
		wantOK    bool
	}{
		{"id only", []string{"jolly-badger"}, []string{"jolly-badger"}, false, true},
		{"id with --fresh", []string{"jolly-badger", "--fresh"}, []string{"jolly-badger"}, true, true},
		{"--fresh before id", []string{"--fresh", "jolly-badger"}, []string{"jolly-badger"}, true, true},
		{"no args", []string{}, nil, false, true},
		{"--fresh alone", []string{"--fresh"}, nil, true, true},
		{"unknown flag", []string{"jolly-badger", "--force"}, nil, false, false},
		{"two positionals pass through", []string{"a", "--fresh", "b"}, []string{"a", "b"}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			devNull, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			defer devNull.Close()
			stderr := os.Stderr
			os.Stderr = devNull // parseIDAndFresh prints its error on the failure path
			rest, fresh, ok := parseIDAndFresh(tc.args, "claudio rebuild")
			os.Stderr = stderr

			if ok != tc.wantOK || fresh != tc.wantFresh || !slices.Equal(rest, tc.wantRest) {
				t.Errorf("parseIDAndFresh(%q) = (%q, %v, %v), want (%q, %v, %v)",
					tc.args, rest, fresh, ok, tc.wantRest, tc.wantFresh, tc.wantOK)
			}
		})
	}
}

// A missing credential must be caught before the multi-minute image
// build starts, not after — the whole command is otherwise a long wait
// ending in a failure that was knowable up front.
func TestRebuildRejectsMissingCredentialBeforeBuilding(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	// Point state somewhere empty so a passing credential check couldn't
	// reach a real instance either way.
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = w
	code := cmdRebuild(context.Background(), []string{"some-instance"})
	os.Stderr = stderr
	w.Close()

	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	r.Close()

	if code == 0 {
		t.Error("cmdRebuild with no credential returned 0, want nonzero")
	}
	out := sb.String()
	if !strings.Contains(out, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("stderr = %q, want it to name the missing credential", out)
	}
	if strings.Contains(out, "Rebuilding image") {
		t.Error("the image build started despite a missing credential; it must fail fast instead")
	}
}

// Docker's CLI prints a "What's next: Try Docker Debug ..." promo when an
// interactive `docker exec -it` exits. attach and logs hand Docker the
// terminal outright (syscall.Exec), so that block lands in the user's
// terminal looking like Claudio's own output — it named the container
// and suggested `docker debug claudio-<id>`, which is not a Claudio
// workflow. DOCKER_CLI_HINTS=false is Docker's opt-out.
func TestDockerExecEnvSuppressesDockerCLIHints(t *testing.T) {
	env := dockerExecEnv()
	if len(env) == 0 || env[0] != "DOCKER_CLI_HINTS=false" {
		t.Fatalf("dockerExecEnv()[0] = %q, want %q", firstOrEmpty(env), "DOCKER_CLI_HINTS=false")
	}
}

// Prepended rather than appended, so a user who has deliberately set
// DOCKER_CLI_HINTS keeps control: exec takes the last value for a
// repeated key, leaving theirs in effect.
func TestDockerExecEnvLetsAnExplicitSettingWin(t *testing.T) {
	t.Setenv("DOCKER_CLI_HINTS", "true")

	env := dockerExecEnv()
	lastIdx, lastVal := -1, ""
	for i, kv := range env {
		if strings.HasPrefix(kv, "DOCKER_CLI_HINTS=") {
			lastIdx, lastVal = i, kv
		}
	}
	if lastIdx <= 0 {
		t.Fatalf("want the user's DOCKER_CLI_HINTS after Claudio's default, got index %d", lastIdx)
	}
	if lastVal != "DOCKER_CLI_HINTS=true" {
		t.Errorf("last DOCKER_CLI_HINTS = %q, want the user's own %q", lastVal, "DOCKER_CLI_HINTS=true")
	}
}

func firstOrEmpty(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
