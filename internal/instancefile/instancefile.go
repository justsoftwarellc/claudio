// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

// Package instancefile reads and writes the per-directory `.claudio`
// file that ties a working directory to the instances created from it
// (ROD-117), so `claudio attach` and friends can infer an id the user
// would otherwise have to copy out of `claudio ls` every time.
//
// It is a pointer, not configuration. Real config lives in the repo's
// committed `.claudio.yml` (internal/config, ROD-113) and the global
// `~/.claudio/config.yml`; this file holds nothing but instance ids.
// Keeping it that way is what lets it be appended to and rewritten
// freely — there is no user-authored content in it to preserve, and no
// schema to migrate.
//
// The file lists ids rather than holding a single one because multiple
// concurrent instances per repo is the architecture's design center, not
// an edge case: one clone per repo, one worktree per session (see
// internal/repo's package doc), and each container hosts exactly one
// agent session (internal/session.SessionName). Parallel streams of work
// therefore mean parallel instances, and one directory routinely has
// several.
package instancefile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the per-directory pointer file. Deliberately distinct from
// the repo's committed `.claudio.yml` and from `~/.claudio/config.yml`:
// the latter is the global config, so naming this one `.claudio/config.yml`
// would have made the same relative path mean two unrelated schemas
// depending on whether you were standing in $HOME.
const FileName = ".claudio"

// file is the on-disk shape. A struct rather than a bare list so the
// file reads as a document with a named key, leaving room to add fields
// later without breaking readers written against today's format.
type file struct {
	Instances []string `yaml:"instances"`
}

// Load reads dir's pointer file. A missing file is not an error — most
// directories simply have no instances — and returns no ids with an
// empty path. A malformed or unknown-keyed file IS an error, matching
// internal/config's stance that a config mistake should be reported
// rather than silently ignored.
//
// The returned path is the file that was read, for error messages that
// need to name it.
func Load(dir string) (ids []string, path string, err error) {
	path = filepath.Join(dir, FileName)

	// A .claudio *directory* is not a pointer file: ~/.claudio is
	// Claudio's own state dir (global config, repos, state.db), and Find
	// walks up through $HOME on its way to the filesystem root. Reading it
	// as a file fails with "is a directory", which would break every bare
	// command run anywhere under $HOME.
	if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		return nil, "", nil
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("instancefile: read %s: %w", path, err)
	}

	// An empty file means "no instances here," not a parse failure:
	// yaml.Decode returns io.EOF for empty input, which is not a
	// malformed-document error worth surfacing to the user.
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, path, nil
	}

	var f file
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, path, fmt.Errorf("instancefile: parse %s: %w", path, err)
	}

	for _, id := range f.Instances {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, path, nil
}

// Find walks up from startDir looking for the nearest pointer file and
// returns the directory holding it along with its ids. The nearest file
// wins, so a nested project inside a parent project resolves to its own
// instances rather than the parent's.
//
// No file anywhere returns ("", nil, nil) — the common case for a
// directory Claudio has never been run in, and not an error.
func Find(startDir string) (dir string, ids []string, err error) {
	current, err := filepath.Abs(startDir)
	if err != nil {
		return "", nil, fmt.Errorf("instancefile: resolve %s: %w", startDir, err)
	}

	for {
		if info, statErr := os.Stat(filepath.Join(current, FileName)); statErr == nil && !info.IsDir() {
			ids, _, err := Load(current)
			if err != nil {
				return "", nil, err
			}
			return current, ids, nil
		}

		parent := filepath.Dir(current)
		if parent == current { // reached the filesystem root
			return "", nil, nil
		}
		current = parent
	}
}

// Append records id in dir's pointer file, creating it if needed, and
// ensures the file is git-ignored. Appending the same id twice is a
// no-op, so a caller never has to check first.
//
// New ids go on the end, keeping the file in creation order — which is
// the order a user listing them expects, and makes the first entry the
// oldest rather than whichever one was written last.
func Append(dir, id string) error {
	ids, _, err := Load(dir)
	if err != nil {
		return err
	}
	for _, existing := range ids {
		if existing == id {
			return ensureIgnored(dir)
		}
	}

	if err := write(dir, append(ids, id)); err != nil {
		return err
	}
	return ensureIgnored(dir)
}

// Remove drops id from dir's pointer file. A missing file, or an id that
// isn't in it, is not an error: `claudio destroy` calls this for every
// instance it destroys, including ones never tied to a directory.
//
// Removing the last id deletes the file outright rather than leaving an
// empty `instances: []` behind — an empty file would still stop Find's
// walk at this directory, shadowing a parent that may legitimately hold
// instances of its own.
func Remove(dir, id string) error {
	ids, path, err := Load(dir)
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

	if len(kept) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("instancefile: remove %s: %w", path, err)
		}
		return nil
	}
	return write(dir, kept)
}

func write(dir string, ids []string) error {
	path := filepath.Join(dir, FileName)
	data, err := yaml.Marshal(file{Instances: ids})
	if err != nil {
		return fmt.Errorf("instancefile: encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("instancefile: write %s: %w", path, err)
	}
	return nil
}

// ensureIgnored adds the pointer file to dir's .gitignore. The ids in it
// are machine-local — they mean nothing in anyone else's store — so the
// file must never be committed.
//
// This appends a single line rather than writing a self-ignoring
// `.claudio/.gitignore`, because the pointer is a file, not a directory,
// and so has nowhere of its own to hide. Existing content is preserved
// verbatim: this is a file the user owns and may well have committed.
// A failure here is not fatal to the caller — see the call sites in
// cmd/claudio, which warn rather than failing a create that otherwise
// succeeded.
func ensureIgnored(dir string) error {
	path := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("instancefile: read %s: %w", path, err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == FileName {
			return nil
		}
	}

	// A file whose last line has no trailing newline would otherwise get
	// the entry glued onto it ("dist/.claudio"), silently ignoring the
	// wrong path and not ignoring ours.
	var buf bytes.Buffer
	buf.Write(data)
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		buf.WriteByte('\n')
	}
	buf.WriteString(FileName + "\n")

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("instancefile: write %s: %w", path, err)
	}
	return nil
}
