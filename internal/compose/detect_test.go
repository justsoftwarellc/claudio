// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package compose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindComposeFileReturnsEmptyWhenNoneExist(t *testing.T) {
	dir := t.TempDir()
	if got := FindComposeFile(dir); got != "" {
		t.Errorf("FindComposeFile = %q, want empty for a directory with no compose file", got)
	}
}

func TestFindComposeFileTriesEachRecognizedName(t *testing.T) {
	for _, name := range composeFileNames {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("services: {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := FindComposeFile(dir); got != path {
				t.Errorf("FindComposeFile = %q, want %q", got, path)
			}
		})
	}
}

func TestFindComposeFileIgnoresADirectoryWithTheSameName(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "compose.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindComposeFile(dir); got != "" {
		t.Errorf("FindComposeFile = %q, want empty — compose.yaml here is a directory, not a file", got)
	}
}
