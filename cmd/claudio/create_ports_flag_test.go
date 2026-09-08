package main

import (
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/portdetect"
)

func TestParseManualPortsBareContainerPorts(t *testing.T) {
	got, err := parseManualPorts("9229,5432")
	if err != nil {
		t.Fatalf("parseManualPorts: %v", err)
	}
	want := []portdetect.Manual{{Container: 9229}, {Container: 5432}}
	if len(got) != len(want) {
		t.Fatalf("parseManualPorts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseManualPortsEmptyFlagAndBlankEntries(t *testing.T) {
	got, err := parseManualPorts("")
	if err != nil || got != nil {
		t.Errorf(`parseManualPorts("") = %+v, %v; want nil, nil`, got, err)
	}
	// A trailing comma is a plausible typo, not an error worth failing on.
	got, err = parseManualPorts("9229, ,")
	if err != nil {
		t.Fatalf("parseManualPorts with blank entries: %v", err)
	}
	if len(got) != 1 || got[0].Container != 9229 {
		t.Errorf("parseManualPorts = %+v, want just container 9229", got)
	}
}

// TestParseManualPortsRejectsHostSide is the behavior change: the
// container:host form used to parse, validate, and then silently discard
// the host side, so `--ports 9229:9229` read as a pin that never pinned
// anything. Claudio allocates host ports itself (store.AllocatePort), so
// the form is rejected with an error that says what to use instead.
func TestParseManualPortsRejectsHostSide(t *testing.T) {
	_, err := parseManualPorts("9229:9229")
	if err == nil {
		t.Fatal("parseManualPorts(\"9229:9229\") succeeded, want a rejection")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"9229"`) {
		t.Errorf("error does not suggest the bare-container form to use instead: %q", msg)
	}
	if !strings.Contains(msg, "ports.range") {
		t.Errorf("error does not explain that Claudio picks the host port: %q", msg)
	}
}

func TestParseManualPortsRejectsNonNumericContainerPort(t *testing.T) {
	if _, err := parseManualPorts("web"); err == nil {
		t.Error(`parseManualPorts("web") succeeded, want an error`)
	}
}
