// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadInstances reads the ids dir's .claudio.yml ties to this directory.
// A missing file is not an error — most directories simply have no
// instances — and returns no ids with an empty path. A malformed file IS
// an error, matching LoadRepoConfig's stance that a config mistake should
// be reported rather than silently ignored.
//
// The returned path is the file that was read, for error messages that
// need to name it.
func LoadInstances(dir string) (ids []string, path string, err error) {
	path = filepath.Join(dir, FileName)

	cfg, err := LoadRepoConfig(path)
	if err != nil {
		return nil, path, err
	}
	if _, statErr := os.Stat(path); statErr != nil {
		// LoadRepoConfig reads a missing file as empty config; distinguish
		// that from a real file holding no `instances:` key, because a
		// caller showing the path must not print one that doesn't exist.
		return nil, "", nil
	}

	for _, id := range cfg.Instances {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, path, nil
}

// FindInstances walks up from startDir looking for the nearest
// .claudio.yml that lists instances, returning the directory holding it
// along with its ids. The nearest wins, so a nested project inside a
// parent project resolves to its own instances rather than the parent's.
//
// The walk skips a .claudio.yml with no `instances:` key rather than
// stopping at it. That distinction did not exist when ids lived in a
// separate file, and it matters now: a repo may legitimately carry image
// or ports config in a subdirectory without any instance ever having been
// created there, and stopping the walk on that file would shadow a parent
// directory that does have instances.
//
// No file anywhere returns ("", nil, nil) — the common case for a
// directory Claudio has never been run in, and not an error.
func FindInstances(startDir string) (dir string, ids []string, err error) {
	current, err := filepath.Abs(startDir)
	if err != nil {
		return "", nil, fmt.Errorf("config: resolve %s: %w", startDir, err)
	}

	for {
		// A .claudio.yml *directory* is not config. ~/.claudio is
		// Claudio's own state dir and this walk passes through $HOME on
		// its way to the filesystem root; the name differs, but the stat
		// guard costs nothing and keeps a stray directory from failing
		// every bare command run beneath it.
		if info, statErr := os.Stat(filepath.Join(current, FileName)); statErr == nil && !info.IsDir() {
			found, _, loadErr := LoadInstances(current)
			if loadErr != nil {
				return "", nil, loadErr
			}
			if len(found) > 0 {
				return current, found, nil
			}
		}

		parent := filepath.Dir(current)
		if parent == current { // reached the filesystem root
			return "", nil, nil
		}
		current = parent
	}
}

// AppendInstance records id in dir's .claudio.yml, creating the file if
// needed, and ensures it is git-ignored. Appending the same id twice is a
// no-op, so a caller never has to check first.
//
// New ids go on the end, keeping them in creation order — the order a
// user listing them expects, and what makes the first entry the oldest
// rather than whichever was written last.
//
// The edit goes through the YAML node tree rather than a marshal of the
// decoded struct. That is not a stylistic choice: this file is
// user-authored, and round-tripping it through RepoConfig would silently
// discard every comment and reorder every key the moment a user ran
// `claudio create .` in a repo whose config they had hand-written.
func AppendInstance(dir, id string) error {
	ids, _, err := LoadInstances(dir)
	if err != nil {
		return err
	}
	for _, existing := range ids {
		if existing == id {
			return ensureIgnored(dir)
		}
	}

	if err := writeInstances(dir, append(ids, id)); err != nil {
		return err
	}
	return ensureIgnored(dir)
}

// RemoveInstance drops id from dir's .claudio.yml. A missing file, or an
// id that isn't listed, is not an error: `claudio destroy` calls this for
// every instance it destroys, including ones never tied to a directory.
//
// Removing the last id drops the `instances:` key but keeps the file —
// the opposite of what a dedicated pointer file could do, and
// necessarily so.
// That file held nothing else, so deleting it was free; this one also
// holds the user's image, ports and hook config, which unlinking an
// instance has no business destroying.
func RemoveInstance(dir, id string) error {
	ids, path, err := LoadInstances(dir)
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}

	kept := make([]string, 0, len(ids))
	for _, existing := range ids {
		if existing != id {
			kept = append(kept, existing)
		}
	}
	if len(kept) == len(ids) {
		return nil // id wasn't listed; nothing to rewrite
	}
	return writeInstances(dir, kept)
}

// writeInstances sets the `instances:` key to ids, preserving everything
// else in the file verbatim. An empty ids removes the key outright rather
// than leaving `instances: []` behind, so a directory with no instances
// reads the same whether it never had one or lost its last.
func writeInstances(dir string, ids []string) error {
	path := filepath.Join(dir, FileName)

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("config: read %s: %w", path, err)
	}

	var root yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	doc := documentMapping(&root)

	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, id := range ids {
		seq.Content = append(seq.Content, scalar(id))
	}

	switch existing := valueFor(doc, "instances"); {
	case existing != nil && len(ids) == 0:
		removeKey(doc, "instances")
	case existing != nil:
		*existing = *seq
	case len(ids) > 0:
		doc.Content = append(doc.Content, scalar("instances"), seq)
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

// removeKey drops a key and its value from a mapping node. Mapping
// content is a flat key/value slice, so both entries go together.
func removeKey(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

// ensureIgnored adds .claudio.yml to dir's .gitignore.
//
// The file is machine-local: the ids in it mean nothing in anyone else's
// store, and the rest describes how this machine runs the project. So it
// must never be committed, and Claudio says so on the user's behalf the
// first time it writes one.
//
// Existing content is preserved verbatim — this is a file the user owns
// and may well have committed. A failure here is not fatal to the caller;
// see the call sites in cmd/claudio, which warn rather than failing a
// create that otherwise succeeded.
func ensureIgnored(dir string) error {
	path := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("config: read %s: %w", path, err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == FileName {
			return nil
		}
	}

	// A file whose last line has no trailing newline would otherwise get
	// the entry glued onto it ("dist/.claudio.yml"), silently ignoring the
	// wrong path and not ignoring ours.
	var buf bytes.Buffer
	buf.Write(data)
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		buf.WriteByte('\n')
	}
	buf.WriteString(FileName + "\n")

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}
