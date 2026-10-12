// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SetGlobalEditor writes `editor: <binary>` into ~/.claudio/config.yml,
// creating the file (and its directory) if it does not exist yet.
//
// This is the first thing in Claudio that writes the *global* config
// rather than a repo's .claudio.yml, and it is edited as a yaml.Node
// tree for exactly the reason AddPortToRepoConfig gives: unmarshalling
// into GlobalConfig and re-marshalling would silently discard every key
// this Go type does not model, along with the user's comments and
// formatting. That is a worse failure here than in a repo file — a
// round-trip through the struct would quietly rewrite the machine's
// resource ceilings and port range as a side effect of setting an
// editor, and Defaults() would materialize as explicit keys the user
// never wrote.
//
// An existing `editor:` is replaced in place, so re-running this is how
// the value is changed rather than a second key being appended.
func SetGlobalEditor(path string, editor string) error {
	return editGlobalConfig(path, func(_ GlobalConfig, doc *yaml.Node) error {
		if existing := valueFor(doc, "editor"); existing != nil {
			// Replace the value node in place rather than the key/value pair,
			// which keeps the key's position and any comment attached to it.
			existing.Kind = yaml.ScalarNode
			existing.Tag = "!!str"
			existing.Value = editor
			existing.Content = nil
		} else {
			doc.Content = append(doc.Content, scalar("editor"), scalar(editor))
		}
		return nil
	})
}

// errNoConfigChange is what an edit returns to leave config.yml exactly
// as it is on disk — a no-op must not re-render the user's formatting.
var errNoConfigChange = errors.New("config: no change")

// editGlobalConfig applies edit to config.yml's top-level mapping and
// writes the result back, creating the file (and its directory) if it
// does not exist yet. edit also gets the file as it decoded before the
// change; an error from it leaves the file untouched.
func editGlobalConfig(path string, edit func(existing GlobalConfig, doc *yaml.Node) error) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("config: read %s: %w", path, err)
	}

	// Parse strictly before editing: a config.yml with a typo'd key is
	// already broken, and appending to it would bury the real problem
	// under a second one the user did not cause.
	var existing GlobalConfig
	if len(data) > 0 {
		if err := decodeStrict(data, &existing); err != nil {
			return fmt.Errorf("config: parse %s: %w", path, err)
		}
	}

	var root yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	doc := documentMapping(&root)

	if err := edit(existing, doc); err != nil {
		if err == errNoConfigChange {
			return nil
		}
		return err
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("config: render %s: %w", path, err)
	}
	// The directory can legitimately be absent: `claudio config set editor`
	// is reachable before any instance has been created, which is what
	// otherwise creates ~/.claudio.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}
