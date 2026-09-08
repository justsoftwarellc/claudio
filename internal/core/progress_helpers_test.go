package core

import "testing"

func TestShortContainerIDTruncatesLongID(t *testing.T) {
	full := "12caa65b347cf123a03f706f1e1e48e4e99622fbf5895b0a5aa6b5cbd7d7654d"
	got := shortContainerID(full)
	want := "12caa65b347c"
	if got != want {
		t.Errorf("shortContainerID(%q) = %q, want %q", full, got, want)
	}
	if len(got) != 12 {
		t.Errorf("shortContainerID result has length %d, want 12", len(got))
	}
}

func TestShortContainerIDLeavesShortIDUnchanged(t *testing.T) {
	short := "abc123"
	if got := shortContainerID(short); got != short {
		t.Errorf("shortContainerID(%q) = %q, want unchanged %q", short, got, short)
	}
}
