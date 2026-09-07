// Command claudio is the CLI for the orchestrator. See
// docs/architecture.md — this package owns ALL terminal I/O (printing,
// exit codes, flag parsing); internal/core stays free of it so a phase-2
// daemon can wrap the same operations without a rewrite (§12.4).
package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 1
	}

	ctx := context.Background()

	switch args[0] {
	case "ls":
		return cmdLs(ctx, args[1:])
	case "create":
		return cmdCreate(ctx, args[1:])
	case "attach":
		return cmdAttach(ctx, args[1:])
	case "destroy":
		return cmdDestroy(ctx, args[1:])
	case "cd":
		return cmdCd(ctx, args[1:])
	case "adopt":
		return cmdAdopt(ctx, args[1:])
	case "forget":
		return cmdForget(ctx, args[1:])
	case "daemon":
		return cmdDaemon(ctx, args[1:])
	case "help", "-h", "--help":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "claudio: unknown command %q\n\n", args[0])
		printUsage()
		return 1
	}
}

func printUsage() {
	fmt.Println(`claudio — sandboxed Claude Code session orchestrator

Usage:
  claudio create <repo> [--branch B | --new-branch B] [--name N] [--ports c:h,...]
                                   provision a new sandboxed session
  claudio ls [--all] [--json]     list instances (phase 1: sorted by creation time)
  claudio attach <id>             attach to an instance's Claude Code session
  claudio cd <id>                 print an instance's workspace path
  claudio destroy <id> [--keep-workspace]
                                   remove an instance's container (and worktree)
  claudio adopt <container>       reconcile an untracked container into the store
  claudio forget <container>      remove an untracked container permanently
  claudio daemon status           report daemon status (phase 1: no daemon yet — see ROD-95)
  claudio help                    show this message

Not yet implemented (see docs/architecture.md, phasing): status, ports,
stop/start/restart.`)
}
