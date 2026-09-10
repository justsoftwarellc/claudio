// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package portdetect

import "encoding/json"

// containsDependencyKey reports whether dep appears as a key in either the
// dependencies or devDependencies object of a package.json file. A
// react-scripts app declares it in one or the other depending on whether
// it was ejected, so both are checked. Malformed JSON is treated as "no
// match" rather than an error — detection is a best-effort guess
// (docs/architecture.md §6.1), not a build step that should fail the repo.
func containsDependencyKey(data []byte, dep string) bool {
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	if _, ok := pkg.Dependencies[dep]; ok {
		return true
	}
	_, ok := pkg.DevDependencies[dep]
	return ok
}
