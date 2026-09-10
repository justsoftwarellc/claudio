// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/rodrigomorales/claudio/internal/coreerr"
)

func TestDescribeErrRewritesBareNotFoundToNoSuchInstance(t *testing.T) {
	// Reproduces internal/store/instances.go's actual wrapping
	// ("store: get instance %q: %w" around sql.ErrNoRows) rather than
	// asserting against a literal string match.
	cause := fmt.Errorf("store: get instance %q: %w", "brave-nonexistent", sql.ErrNoRows)
	err := coreerr.Wrap(coreerr.NotFound, "core: status brave-nonexistent", cause)

	got := describeErr(err)
	if got != "no such instance" {
		t.Errorf("describeErr(%v) = %q, want %q", err, got, "no such instance")
	}
}

func TestDescribeErrPassesThroughHandWrittenNotFoundMessages(t *testing.T) {
	// NotFound is also used for messages already written to be read
	// directly (internal/core/adopt.go, internal/core/image.go) — these
	// must not be clobbered by the bare-ErrNoRows rewrite.
	err := coreerr.Wrap(coreerr.NotFound, "core: provision",
		errors.New(`image "claudio/repo-foo:latest" does not exist — run `+"`claudio image build --repo /x`"+` to build it`))
	got := describeErr(err)
	want := `image "claudio/repo-foo:latest" does not exist — run ` + "`claudio image build --repo /x`" + ` to build it`
	if got != want {
		t.Errorf("describeErr(%v) = %q, want %q (hand-written NotFound message must pass through)", err, got, want)
	}
}

func TestDescribeErrPassesThroughNonCoreerrErrors(t *testing.T) {
	err := errors.New("--repo requires a value")
	if got := describeErr(err); got != "--repo requires a value" {
		t.Errorf("describeErr(%v) = %q, want the error's own message unchanged", err, got)
	}
}
