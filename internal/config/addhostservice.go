// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// AddHostServiceToRepoConfig records a `claudio create --host-service`
// declaration in the instance worktree's .claudio.yml (ROD-128).
//
// This exists for the same reason AddPortToRepoConfig does, and guards
// against the same bug (ROD-123): .claudio.yml — not the store — is what
// a restart re-derives an instance's configuration from, and host
// services are deliberately not stored at all (nothing is allocated, so
// there is no reservation to record). Without writing the flag down, a
// --host-service passed at create would work exactly until the first
// `claudio restart` and then silently stop resolving, which is the worst
// possible shape for a networking failure.
//
// An entry whose name already exists is *updated* rather than rejected,
// unlike AddPortToRepoConfig's duplicate handling: the flag layer wins
// over the repo's own declaration by design (see core.resolveHostServices
// on why the person at the keyboard outranks a file that arrived with a
// clone), so "--host-service db:6000 against a repo declaring db:5432"
// is a redirect the user asked for, not a conflict to report.
//
// The file is edited as a yaml.Node tree for the same reason
// AddPortToRepoConfig does it that way — see that function's doc.
func AddHostServiceToRepoConfig(dir string, hs HostService) error {
	path := dir + "/.claudio.yml"

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

	entryContent := []*yaml.Node{
		scalar("name"), scalar(hs.Name),
		scalar("host"), intScalar(hs.Host),
	}
	// Only written when the user actually supplied it: an emitted
	// `container:` equal to `host:` would be noise in a file the user
	// owns, and re-reading it must produce the same HostService either
	// way (HostService.ContainerPort defaults Container to Host).
	if hs.Container != nil {
		entryContent = append(entryContent, scalar("container"), intScalar(*hs.Container))
	}
	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: entryContent}

	list := valueFor(doc, "host_services")
	if list == nil {
		doc.Content = append(doc.Content, scalar("host_services"), &yaml.Node{
			Kind:    yaml.SequenceNode,
			Tag:     "!!seq",
			Content: []*yaml.Node{entry},
		})
	} else {
		// An explicit `host_services:` with no entries parses as a null
		// scalar, not an empty sequence; turn it into one before appending.
		if list.Kind != yaml.SequenceNode {
			list.Kind = yaml.SequenceNode
			list.Tag = "!!seq"
			list.Value = ""
			list.Content = nil
		}
		replaced := false
		for i, existing := range list.Content {
			if name := valueFor(existing, "name"); name != nil && name.Value == hs.Name {
				list.Content[i] = entry
				replaced = true
				break
			}
		}
		if !replaced {
			list.Content = append(list.Content, entry)
		}
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
