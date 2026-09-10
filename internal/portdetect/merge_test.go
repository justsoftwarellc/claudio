// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package portdetect

import (
	"testing"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/store"
)

func TestMergeDetectedOnly(t *testing.T) {
	detected := []Detected{{ServiceName: "web", Container: 3000, From: "next.config.js"}}

	got, err := Merge(detected, nil, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(got) != 1 || got[0].Source != store.PortDetected || got[0].DetectedFrom == nil || *got[0].DetectedFrom != "next.config.js" {
		t.Fatalf("Merge = %+v, want single detected mapping", got)
	}
}

func TestMergeDeclaredWinsOverDetectedOnConflict(t *testing.T) {
	detected := []Detected{{ServiceName: "web", Container: 3000, From: "next.config.js"}}
	declared := []config.Port{{Name: "frontend", Container: 3000}}

	got, err := Merge(detected, declared, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Merge = %+v, want one merged mapping for the conflicting port", got)
	}
	if got[0].Source != store.PortDeclared || got[0].ServiceName != "frontend" {
		t.Fatalf("Merge = %+v, want declared source to win with its own service name", got)
	}
}

func TestMergeDeclaredExposeFalse(t *testing.T) {
	no := false
	declared := []config.Port{{Name: "db", Container: 5432, Expose: &no}}

	got, err := Merge(nil, declared, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(got) != 1 || got[0].Expose {
		t.Fatalf("Merge = %+v, want Expose=false", got)
	}
}

func TestMergeManualSupplementsRatherThanReplaces(t *testing.T) {
	detected := []Detected{{ServiceName: "web", Container: 3000, From: "next.config.js"}}
	manual := []Manual{{Container: 9229}}

	got, err := Merge(detected, nil, manual)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Merge = %+v, want detected port kept plus the new manual one", got)
	}
}

func TestMergeManualOverridesOnConflict(t *testing.T) {
	detected := []Detected{{ServiceName: "web", Container: 3000, From: "next.config.js"}}
	manual := []Manual{{Container: 3000, ServiceName: "debug-web"}}

	got, err := Merge(detected, nil, manual)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Merge = %+v, want only one mapping for the conflicting port", got)
	}
	if got[0].Source != store.PortManual || got[0].ServiceName != "debug-web" {
		t.Fatalf("Merge = %+v, want manual to win the conflicting port", got)
	}
}

func TestMergeManualDefaultServiceName(t *testing.T) {
	got, err := Merge(nil, nil, []Manual{{Container: 9229}})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(got) != 1 || got[0].ServiceName != "manual-9229" {
		t.Fatalf("Merge = %+v, want default service name manual-9229", got)
	}
}

func TestMergePreservesFirstSeenOrder(t *testing.T) {
	detected := []Detected{
		{ServiceName: "web", Container: 3000, From: "next.config.js"},
		{ServiceName: "api", Container: 8080, From: "go.mod"},
	}
	declared := []config.Port{{Name: "db", Container: 5432}}

	got, err := Merge(detected, declared, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	wantOrder := []int{3000, 8080, 5432}
	if len(got) != len(wantOrder) {
		t.Fatalf("Merge = %+v, want %d mappings", got, len(wantOrder))
	}
	for i, w := range wantOrder {
		if got[i].Container != w {
			t.Errorf("got[%d].Container = %d, want %d", i, got[i].Container, w)
		}
	}
}

func TestMergeDeclaredMissingNameErrors(t *testing.T) {
	_, err := Merge(nil, []config.Port{{Container: 3000}}, nil)
	if err == nil {
		t.Fatal("expected error for declared port missing a name")
	}
}
