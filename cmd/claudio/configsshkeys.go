// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rodrigomorales/claudio/internal/config"
)

// cmdConfigAdd implements `claudio config add <key> <value>` — the
// list-valued counterpart to cmdConfigSet, for a setting where "set"
// would have to mean either "replace the whole list" or "append", and
// neither is what someone adding a second key expects to have guessed.
//
// Only `ssh_keys` exists so far: the private keys mounted into every
// instance so git inside one can reach an SSH remote.
func cmdConfigAdd(ctx context.Context, args []string) int {
	key, ok := sshKeyArg("add", args)
	if !ok {
		return 1
	}
	// A public key is the likeliest wrong file to pass — it sits next to
	// the right one and tab-completes after it — and it would mount
	// cleanly and then authenticate nothing.
	if strings.HasSuffix(key, ".pub") {
		fmt.Fprintf(os.Stderr, "claudio config add ssh_keys: %s is a public key\n"+
			"  pass the private key (the same path without .pub)\n", key)
		return 1
	}

	path, err := globalConfigPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio config add:", err)
		return 1
	}
	// Validated before writing, like `config set editor`: a typo'd path
	// stored now would fail every later create/start/restart instead.
	added, err := config.AddGlobalSSHKey(path, key)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio config add:", err)
		return 1
	}
	if !added {
		fmt.Printf("ssh_keys already lists %s\n%s\n", key, path)
		return 0
	}
	fmt.Printf("ssh_keys += %s\n%s\n", key, path)
	// The mount is made when a container is created, so a running
	// instance does not see the key until it is replaced.
	fmt.Println("New instances get it; apply it to a running one with: claudio restart <id>")
	return 0
}

// cmdConfigRemove implements `claudio config remove <key> <value>`.
//
// Unlike add, the path is not required to exist: removing the entry for
// a key that has since been deleted from disk is exactly the repair
// this is needed for.
func cmdConfigRemove(ctx context.Context, args []string) int {
	key, ok := sshKeyArg("remove", args)
	if !ok {
		return 1
	}
	path, err := globalConfigPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio config remove:", err)
		return 1
	}
	removed, err := config.RemoveGlobalSSHKey(path, key)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio config remove:", err)
		return 1
	}
	if !removed {
		fmt.Fprintf(os.Stderr, "claudio config remove ssh_keys: %s is not listed\n"+
			"  see what is with: claudio config get ssh_keys\n", key)
		return 1
	}
	fmt.Printf("ssh_keys -= %s\n%s\n", key, path)
	fmt.Println("A running instance keeps the key until: claudio restart <id>")
	return 0
}

// sshKeyArg parses `<verb> ssh_keys <path>` and returns the path as it
// should be stored: absolute, so the entry means the same thing whatever
// directory a later command runs from. A leading "~" is kept as typed
// (it only survives the shell when quoted) since the config expands it.
func sshKeyArg(verb string, args []string) (string, bool) {
	usage := fmt.Sprintf("Usage:\n  claudio config %s ssh_keys <path>", verb)
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "claudio config %s: expected a key\n\n%s\n", verb, usage)
		return "", false
	}
	if args[0] != "ssh_keys" {
		fmt.Fprintf(os.Stderr, "claudio config %s: unknown key %q (known keys: ssh_keys)\n", verb, args[0])
		return "", false
	}
	if len(args) != 2 {
		fmt.Fprintf(os.Stderr, "claudio config %s ssh_keys: expected exactly one path, got %d\n\n%s\n", verb, len(args)-1, usage)
		return "", false
	}
	key := args[1]
	if key == "~" || strings.HasPrefix(key, "~/") {
		return key, true
	}
	abs, err := filepath.Abs(key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "claudio config %s ssh_keys: %v\n", verb, err)
		return "", false
	}
	return abs, true
}
