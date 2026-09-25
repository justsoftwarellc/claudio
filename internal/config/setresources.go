// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// SetResourcesInRepoConfig records a `claudio create --memory/--cpus/
// --pids` override in the instance's .claudio.yml (ROD-137). Takes the
// resolved config path rather than a directory, for the reason
// AddPortToRepoConfig's doc gives: where config lives is the store's
// answer to give, not this function's to assume (ROD-133).
//
// This exists for the same reason AddHostServiceToRepoConfig does, and
// guards against the same bug (ROD-123): .claudio.yml — not the store —
// is what a restart re-derives an instance's configuration from, and
// `restart` accepts no resource flags of its own. Without writing the
// override down, `create --memory 12g` held until the first restart and
// then silently dropped back to the global default, with no warning and
// no record that an override had ever been in effect.
//
// Only the fields the user actually typed are written: a nil field means
// "not overridden", and emitting a resolved value for it would freeze
// today's global default into the repo's file, so a later change to
// ~/.claudio/config.yml would stop reaching this instance. Existing keys
// are replaced in place rather than appended, so repeated creates cannot
// accumulate duplicate entries under resources:.
//
// The file is edited as a yaml.Node tree for the same reason
// AddPortToRepoConfig does it that way — see that function's doc.
func SetResourcesInRepoConfig(path string, r Resources) error {
	if r.Memory == nil && r.CPUs == nil && r.PIDs == nil {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("config: read %s: %w", path, err)
	}

	var root yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	doc := documentMapping(&root)

	res := valueFor(doc, "resources")
	if res == nil || res.Kind != yaml.MappingNode {
		// Covers both a missing resources: and an explicit one holding
		// null (`resources:` with nothing under it), which parses as a
		// scalar rather than an empty mapping.
		if res == nil {
			res = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			doc.Content = append(doc.Content, scalar("resources"), res)
		} else {
			res.Kind = yaml.MappingNode
			res.Tag = "!!map"
			res.Value = ""
			res.Content = nil
		}
	}

	if r.Memory != nil {
		setMappingValue(res, "memory", scalar(*r.Memory))
	}
	if r.CPUs != nil {
		setMappingValue(res, "cpus", intScalar(*r.CPUs))
	}
	if r.PIDs != nil {
		setMappingValue(res, "pids", intScalar(*r.PIDs))
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("config: render %s: %w", path, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// setMappingValue replaces key's value in a mapping, appending the pair
// if the key isn't there yet. Replacing in place matters for a file the
// user owns: a second `create --memory` against the same repo must
// update the number rather than leave two `memory:` keys, which is both
// confusing to read and rejected by decodeStrict on the next load.
func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, scalar(key), value)
}
