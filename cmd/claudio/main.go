// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

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
	case "logs":
		return cmdLogs(ctx, args[1:])
	case "destroy":
		return cmdDestroy(ctx, args[1:])
	case "cd":
		return cmdCd(ctx, args[1:])
	case "status":
		return cmdStatus(ctx, args[1:])
	case "ports":
		return cmdPorts(ctx, args[1:])
	case "stop":
		return cmdStop(ctx, args[1:])
	case "start":
		return cmdStart(ctx, args[1:])
	case "restart":
		return cmdRestart(ctx, args[1:])
	case "rebuild":
		return cmdRebuild(ctx, args[1:])
	case "link":
		return cmdLink(ctx, args[1:])
	case "unlink":
		return cmdUnlink(ctx, args[1:])
	case "adopt":
		return cmdAdopt(ctx, args[1:])
	case "forget":
		return cmdForget(ctx, args[1:])
	case "daemon":
		return cmdDaemon(ctx, args[1:])
	case "image":
		return cmdImage(ctx, args[1:])
	case "config":
		return cmdConfig(ctx, args[1:])
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
  claudio create <repo> [--branch B | --new-branch B] [--name N] [--ports c,...]
                        [--publish-all-interfaces] [--memory M] [--cpus N] [--pids N]
                        [--clean-on-fail]
                                   provision a new sandboxed session
  claudio ls [--all]              list instances (--all includes stopped ones)
  claudio attach [<id>]           attach to an instance's Claude Code session
  claudio logs [<id>] [--service X] [--follow]
                                   stream a container's logs (compose instances:
                                   --service names one sidecar, or every service)
  claudio cd [<id>]               print an instance's workspace path
  claudio status [<id>]           show one instance's detail view
  claudio ports [<id>] [--add c] [--remove c]
                                   show or amend an instance's port mappings
  claudio stop [<id>]             remove an instance's container, keep its workspace
  claudio start [<id>] [--fresh]  re-provision a container for a stopped instance
  claudio restart [<id>] [--fresh]
                                   stop then start (resumes the session unless --fresh)
  claudio rebuild [<id>] [--fresh]
                                   rebuild the image, then recreate the container from it
  claudio destroy [<id>] [--keep-workspace]
                                   remove an instance's container (and worktree)
  claudio link [<id>]             tie an existing instance to this directory
  claudio unlink [<id>]           untie one (the instance itself is untouched)
  claudio adopt <container>       reconcile an untracked container into the store
  claudio forget <container>      remove an untracked container permanently
  claudio config restore [<id>] [--json]
                                   rewrite an instance's ~/.claude.json when Claude
                                   Code refuses to start against it (invalid JSON)
  claudio image build [--repo <path>]
                                   build claudio/base:latest (and a repo-specific
                                   layer, if --repo's .claudio.yml asks for one)
  claudio daemon status           report daemon status (phase 1: no daemon yet — see ROD-95)
  claudio help                    show this message

An omitted <id> is read from the nearest .claudio file — written by
"claudio create ." or "claudio link" — so commands run from that
directory need no id. If the directory has several instances, pass
one explicitly.`)
}
