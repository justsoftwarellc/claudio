// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

// EffectiveResources applies the three-layer resolution from
// docs/architecture.md §12.3: global config supplies the baseline, the
// repo's .claudio.yml may raise or lower it, and a local override (if
// present) always wins over both — because "this repo needs 12 GB" is a
// property of the project worth committing, while "this machine only has
// 15.7 GB" is a property of the machine and must be able to override it.
//
// overriddenByLocal reports whether the local layer actually changed the
// resolved *value* (not merely whether a local override was supplied) —
// so callers (ROD-112) can print "local override applied" only when it
// genuinely changed something, rather than on every run where a local
// file happens to exist but agrees with the repo.
func EffectiveResources(global, repo Resources, local *Resources) (effective Resources, overriddenByLocal bool) {
	beforeLocal := global.Merge(repo)
	if local == nil {
		return beforeLocal, false
	}
	afterLocal := beforeLocal.Merge(*local)
	return afterLocal, !resourcesEqual(beforeLocal, afterLocal)
}

func resourcesEqual(a, b Resources) bool {
	return strPtrEqual(a.Memory, b.Memory) &&
		intPtrEqual(a.CPUs, b.CPUs) &&
		intPtrEqual(a.PIDs, b.PIDs)
}

func strPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func intPtrEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
