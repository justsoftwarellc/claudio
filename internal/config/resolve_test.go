// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

import "testing"

func strp(s string) *string { return &s }
func intp(i int) *int       { return &i }

func TestEffectiveResourcesGlobalOnly(t *testing.T) {
	global := Resources{Memory: strp("6g"), CPUs: intp(4)}
	eff, overridden := EffectiveResources(global, Resources{}, nil)
	if *eff.Memory != "6g" || *eff.CPUs != 4 {
		t.Fatalf("expected global baseline, got %+v", eff)
	}
	if overridden {
		t.Fatalf("expected no override reported")
	}
}

func TestEffectiveResourcesRepoRaisesMemory(t *testing.T) {
	global := Resources{Memory: strp("6g"), CPUs: intp(4)}
	repo := Resources{Memory: strp("10g")}
	eff, _ := EffectiveResources(global, repo, nil)
	if *eff.Memory != "10g" {
		t.Fatalf("expected repo override to 10g, got %s", *eff.Memory)
	}
	if *eff.CPUs != 4 {
		t.Fatalf("expected CPUs to remain global default 4, got %d", *eff.CPUs)
	}
}

func TestEffectiveResourcesLocalAlwaysWinsOverRepo(t *testing.T) {
	global := Resources{Memory: strp("6g"), CPUs: intp(4)}
	repo := Resources{Memory: strp("12g")} // repo wants more than the VM has
	local := Resources{Memory: strp("6g")} // machine caps it back down

	eff, overridden := EffectiveResources(global, repo, &local)
	if *eff.Memory != "6g" {
		t.Fatalf("expected local override to win, got %s", *eff.Memory)
	}
	if !overridden {
		t.Fatalf("expected overriddenByLocal=true when local actually changes the value")
	}
}

func TestEffectiveResourcesLocalMatchingRepoIsNotReportedAsOverride(t *testing.T) {
	global := Resources{Memory: strp("6g")}
	repo := Resources{Memory: strp("10g")}
	local := Resources{Memory: strp("10g")} // same value, doesn't actually change anything

	_, overridden := EffectiveResources(global, repo, &local)
	if overridden {
		t.Fatalf("expected overriddenByLocal=false when local matches the already-resolved value")
	}
}
