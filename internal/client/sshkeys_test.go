// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/core"
)

// Create, Start and Restart all provision through resolveCreateParams,
// so this is the one place the global config's ssh_keys: has to reach a
// container spec from — and the one place a bad entry has to stop the
// command, before anything is provisioned.
func TestResolveCreateParamsCarriesConfiguredSSHKeys(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}

	global := config.Defaults()
	if params, err := (&Local{global: global}).resolveCreateParams(core.CreateParams{}); err != nil || len(params.SSHKeys) != 0 {
		t.Fatalf("with no ssh_keys: SSHKeys = %v, err = %v; want none and no error", params.SSHKeys, err)
	}

	global.SSHKeys = []string{key}
	params, err := (&Local{global: global}).resolveCreateParams(core.CreateParams{})
	if err != nil {
		t.Fatalf("resolveCreateParams: %v", err)
	}
	if len(params.SSHKeys) != 1 || params.SSHKeys[0] != key {
		t.Errorf("SSHKeys = %v, want [%s]", params.SSHKeys, key)
	}

	global.SSHKeys = []string{filepath.Join(dir, "gone")}
	if _, err := (&Local{global: global}).resolveCreateParams(core.CreateParams{}); err == nil || !strings.Contains(err.Error(), "ssh_keys") {
		t.Errorf("with a missing key: err = %v, want one naming ssh_keys", err)
	}
}
