// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ResolveSSHKeys turns the global config's ssh_keys: into the host
// paths Docker will bind. A listed key that cannot be mounted is an
// error rather than a skip: the operator named it, so provisioning an
// instance without it would only move the failure to the agent's first
// `git fetch`, where nothing says why.
//
// Symlinks are resolved because Docker binds the path it is given, and a
// dotfiles-managed key that is a link would mount as a dangling one.
// Base names must be unique because that is the name a key is mounted
// under (engine.SSHKeyTarget).
func ResolveSSHKeys(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	seen := make(map[string]string, len(paths))
	for _, configured := range paths {
		expanded, err := expandSSHKeyPath(configured)
		if err != nil {
			return nil, err
		}
		if !filepath.IsAbs(expanded) {
			return nil, fmt.Errorf("config: ssh_keys: %q is not an absolute path (or one starting with ~)", configured)
		}
		resolved, err := filepath.EvalSymlinks(expanded)
		if err != nil {
			return nil, fmt.Errorf("config: ssh_keys: %s: %w", configured, err)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, fmt.Errorf("config: ssh_keys: %s: %w", configured, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("config: ssh_keys: %s is not a file — list each private key, not the directory holding them", configured)
		}
		name := filepath.Base(resolved)
		if other, clash := seen[name]; clash {
			return nil, fmt.Errorf("config: ssh_keys: %s and %s are both named %q, and keys are mounted by file name — rename one", other, configured, name)
		}
		seen[name] = configured
		out = append(out, resolved)
	}
	return out, nil
}

// expandSSHKeyPath resolves a leading "~" against the user's home
// directory, the one shorthand ssh_keys: entries accept.
func expandSSHKeyPath(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: ssh_keys: resolve home directory: %w", err)
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}

// sameSSHKey reports whether two ssh_keys: entries name the same path
// once "~" is expanded, so `~/.ssh/id` and its absolute spelling are
// one entry rather than two. An entry whose home cannot be resolved
// compares as written.
func sameSSHKey(a, b string) bool {
	ea, err := expandSSHKeyPath(a)
	if err != nil {
		ea = a
	}
	eb, err := expandSSHKeyPath(b)
	if err != nil {
		eb = b
	}
	return filepath.Clean(ea) == filepath.Clean(eb)
}

// AddGlobalSSHKey appends key to `ssh_keys:` in ~/.claudio/config.yml,
// creating the file if needed, and reports whether it changed anything
// — false means the key was already listed.
//
// The whole resulting list is validated before writing, not just the
// new entry: two keys with one file name is a property of the list, and
// it is better refused here than at the next `claudio create`.
// RemoveGlobalSSHKey validates nothing, so a list that has since gone
// bad (a key deleted from disk) can always be repaired.
//
// Edited as a yaml.Node tree for the reason SetGlobalEditor gives.
func AddGlobalSSHKey(path string, key string) (bool, error) {
	added := false
	err := editGlobalConfig(path, func(existing GlobalConfig, doc *yaml.Node) error {
		for _, k := range existing.SSHKeys {
			if sameSSHKey(k, key) {
				return errNoConfigChange
			}
		}
		if _, err := ResolveSSHKeys(append(append([]string{}, existing.SSHKeys...), key)); err != nil {
			return err
		}
		seq := valueFor(doc, "ssh_keys")
		if seq == nil {
			seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			doc.Content = append(doc.Content, scalar("ssh_keys"), seq)
		} else if seq.Kind != yaml.SequenceNode {
			// `ssh_keys:` with no value decodes as an empty list but is a
			// null scalar in the tree.
			seq.Kind = yaml.SequenceNode
			seq.Tag = "!!seq"
			seq.Value = ""
		}
		// A list written inline (`ssh_keys: []`) would otherwise grow as
		// one long flow line.
		seq.Style = 0
		seq.Content = append(seq.Content, scalar(key))
		added = true
		return nil
	})
	return added, err
}

// RemoveGlobalSSHKey drops key from `ssh_keys:` and reports whether it
// was there.
func RemoveGlobalSSHKey(path string, key string) (bool, error) {
	removed := false
	err := editGlobalConfig(path, func(_ GlobalConfig, doc *yaml.Node) error {
		seq := valueFor(doc, "ssh_keys")
		if seq == nil || seq.Kind != yaml.SequenceNode {
			return errNoConfigChange
		}
		kept := seq.Content[:0]
		for _, n := range seq.Content {
			if sameSSHKey(n.Value, key) {
				removed = true
				continue
			}
			kept = append(kept, n)
		}
		seq.Content = kept
		if !removed {
			return errNoConfigChange
		}
		return nil
	})
	return removed, err
}
