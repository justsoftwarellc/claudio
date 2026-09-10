// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package coreerr

import (
	"errors"
	"fmt"
	"testing"
)

func TestWrapNilReturnsNilNotTypedNil(t *testing.T) {
	// A classic Go footgun: returning a nil *Error through an error
	// return value produces a non-nil interface. Wrap must return a
	// literal nil so `if err := Wrap(...); err != nil` behaves as any
	// caller expects.
	err := Wrap(NotFound, "core: test", nil)
	if err != nil {
		t.Fatalf("Wrap(_, _, nil) = %v, want nil", err)
	}
}

func TestWrapPreservesCauseForUnwrap(t *testing.T) {
	cause := errors.New("underlying failure")
	err := Wrap(Internal, "core: test", cause)

	if !errors.Is(err, cause) {
		t.Error("errors.Is(wrapped, cause) = false, want true — Cause must be reachable via Unwrap")
	}
}

func TestIsMatchesDirectWrap(t *testing.T) {
	err := Wrap(NotFound, "core: get x", errors.New("no such row"))
	if !Is(err, NotFound) {
		t.Error("Is(err, NotFound) = false, want true")
	}
	if Is(err, Conflict) {
		t.Error("Is(err, Conflict) = true, want false — wrong code")
	}
}

func TestIsMatchesThroughAdditionalWrapping(t *testing.T) {
	// A caller one level up from where coreerr.Wrap was applied often
	// adds its own fmt.Errorf("%w") context (e.g. cmd/claudio prefixing
	// "claudio create: "). Is must still see through that.
	inner := Wrap(Conflict, "core: create x", errors.New("branch taken"))
	outer := fmt.Errorf("claudio create: %w", inner)

	if !Is(outer, Conflict) {
		t.Error("Is(outer, Conflict) = false, want true — must see through additional %w wrapping")
	}
}

func TestIsFalseForPlainError(t *testing.T) {
	if Is(errors.New("plain"), NotFound) {
		t.Error("Is(plain error, NotFound) = true, want false")
	}
	if Is(nil, NotFound) {
		t.Error("Is(nil, NotFound) = true, want false")
	}
}

func TestCodeOfReturnsCodeAndOk(t *testing.T) {
	err := Wrap(ProvisionFailed, "core: create x", errors.New("boom"))

	code, ok := CodeOf(err)
	if !ok {
		t.Fatal("CodeOf: ok = false, want true")
	}
	if code != ProvisionFailed {
		t.Errorf("CodeOf: code = %q, want %q", code, ProvisionFailed)
	}
}

func TestCodeOfFalseForNonCoreerrError(t *testing.T) {
	_, ok := CodeOf(errors.New("plain"))
	if ok {
		t.Error("CodeOf(plain error): ok = true, want false")
	}
}

func TestErrorMessageIncludesOpAndCause(t *testing.T) {
	err := Wrap(NotFound, "core: get brave-otter", errors.New("no rows"))
	got := err.Error()
	want := "core: get brave-otter: no rows"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestErrorMessageWithNilCauseFallsBackToCode(t *testing.T) {
	err := &Error{Code: Internal, Op: "core: something"}
	got := err.Error()
	want := "core: something: internal"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
