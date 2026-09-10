// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"context"
	"strings"
	"testing"
)

// `claudio config` is a namespace, not a verb of its own: a bare
// `claudio config` must say so rather than doing something, and an
// unknown subcommand must name the ones that exist. Both go to stderr
// with a nonzero status so a script can tell the difference.
func TestConfigRequiresAKnownSubcommand(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no subcommand", nil},
		{"unknown subcommand", []string{"reset"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := cmdConfig(context.Background(), tc.args); code == 0 {
				t.Errorf("cmdConfig(%v) = 0, want nonzero", tc.args)
			}
		})
	}
}

// An unknown flag must be refused outright rather than treated as an
// instance id — `claudio config restore --frsh` silently resolving to
// "whatever instance this directory is tied to" is exactly the sort of
// near-miss that makes a repair command untrustworthy.
//
// The check has to happen before any Docker or store work, which is
// what makes this assertable without either: reaching newClient would
// change the failure's shape.
func TestConfigRestoreRejectsUnknownFlags(t *testing.T) {
	if code := cmdConfigRestore(context.Background(), []string{"--nope"}); code == 0 {
		t.Error("cmdConfigRestore(--nope) = 0, want nonzero")
	}
}

// The usage text is the only place a user learns the command exists, so
// a command wired into the dispatcher but missing from help is a real
// (if small) defect — and one nothing else in the suite would catch.
func TestUsageDocumentsConfigRestore(t *testing.T) {
	out := captureStdout(t, printUsage)
	if !strings.Contains(out, "claudio config restore") {
		t.Errorf("usage does not mention `claudio config restore`:\n%s", out)
	}
}
