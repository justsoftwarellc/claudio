package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rodrigomorales/claudio/internal/client"
	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/store"
)

// newClient builds the phase-1 Client. See docs/architecture.md §12.4:
// once a daemon exists this becomes a socket-existence check that
// returns remote.Client instead — every command in this package is
// written against the Client interface precisely so that swap needs no
// other change here.
func newClient(ctx context.Context) (client.Client, error) {
	claudioDir, err := claudioStateDir()
	if err != nil {
		return nil, err
	}

	global, err := config.LoadGlobalConfig(filepath.Join(claudioDir, "config.yml"))
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(claudioDir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", claudioDir, err)
	}

	st, err := store.Open(ctx, filepath.Join(claudioDir, "state.db"))
	if err != nil {
		return nil, err
	}

	return client.NewLocal(st, global), nil
}

// claudioStateDir resolves where Claudio keeps its state. CLAUDIO_HOME
// overrides it directly — used for tests and for anyone running more
// than one isolated Claudio state on the same machine; the default is
// ~/.claudio (docs/architecture.md §5.1).
func claudioStateDir() (string, error) {
	if v := os.Getenv("CLAUDIO_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".claudio"), nil
}
