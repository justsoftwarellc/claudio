package core

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/store"
)

// TestAddPortExhaustedRangeNamesRangeAndConfig covers ROD-98's "Exhausted
// range is a clear error naming the range and how to widen it, not a
// cryptic bind failure." The range is squeezed to a single port and then
// consumed, so the second allocation has nowhere to go.
func TestAddPortExhaustedRangeNamesRangeAndConfig(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	// A one-port range: the first --add consumes it, the second cannot.
	const low, high = 43917, 43917
	if _, err := AddPort(context.Background(), s, result.InstanceID, 9229, low, high); err != nil {
		t.Fatalf("first AddPort (should fit in the one-port range): %v", err)
	}

	_, err = AddPort(context.Background(), s, result.InstanceID, 9230, low, high)
	if err == nil {
		t.Fatal("second AddPort succeeded, want the range to be exhausted")
	}
	if !errors.Is(err, store.ErrPortRangeExhausted) {
		t.Errorf("errors.Is(err, store.ErrPortRangeExhausted) = false, want true (err = %v)", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "43917-43917") {
		t.Errorf("error does not name the exhausted range: %q", msg)
	}
	if !strings.Contains(msg, "ports.range") || !strings.Contains(msg, "~/.claudio/config.yml") {
		t.Errorf("error does not say how to widen the range: %q", msg)
	}
}

// TestDescribePortRangeExhaustedPassesOtherErrorsThrough guards against
// the wrapper turning every allocation failure into a "widen your range"
// message — a store or SQLite error is not a capacity problem and must
// reach the user as itself.
func TestDescribePortRangeExhaustedPassesOtherErrorsThrough(t *testing.T) {
	other := errors.New("store: acquire connection: database is locked")
	got := describePortRangeExhausted(other, 43000, 43999)
	if got != other {
		t.Errorf("describePortRangeExhausted rewrote an unrelated error: %v", got)
	}
}
