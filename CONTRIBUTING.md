# Contributing to claudio

Thanks for considering a contribution.

## Licensing — read this first

claudio is licensed under the [PolyForm Noncommercial License 1.0.0](LICENSE):
free for noncommercial use, with commercial use reserved. That reservation is
deliberate — it keeps the door open to sell commercial licenses to businesses.

Because of that, contributions need a [Contributor License Agreement](CLA.md).
The short version:

- **You keep the copyright in your work.** The CLA is a license grant, not an
  assignment. Your contribution stays yours to use, publish, or relicense
  however you want, with no obligation here.
- **You grant the right to sublicense.** This is the part that matters: it lets
  your contribution be included when claudio is licensed commercially. Without
  it, contributed code would have to be rewritten or routed around before it
  could go into a commercial license.

Accepting is one checkbox in the pull request template. It covers that pull
request and any future ones, so you only do it once.

If your employer has rights to code you write, check with them first — CLA
Section 4 asks you to confirm you have permission.

## Development

```bash
./install.sh          # checks dependencies, builds, installs, builds base image
./install.sh --check  # report what's missing, change nothing
go build ./...
go test ./...
```

Tests that need a real container runtime skip themselves when it isn't there,
so `go test ./...` works without Docker running — it just covers less. To
exercise the live paths, build the test image first:

```bash
docker build -t claudio/base:dev image/
go test ./...
```

`go test -short ./...` skips the slowest container-backed tests.

Before opening a pull request:

- `gofmt -l .` reports nothing
- `go vet ./...` is clean
- `go test ./...` passes
- New files carry the SPDX header: `// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0`

## Changes

Every behavior change needs a test. Prefer a test that documents an edge case
over a comment describing it.

Commit subjects say what changed and why, in that order — "Fix the port
allocator spinning on one unbindable port", "Make `ports --add` survive the
restart it tells you to run". `git log` is the reference for the house style.

Pull requests use a [template](.github/pull_request_template.md) with two
sections. **Why?** is the problem being solved. **What?** is the change, plus
anything a reviewer would otherwise have to reverse-engineer — trade-offs made,
approaches rejected, why the tests cover what they cover.

[`docs/architecture.md`](docs/architecture.md) explains how claudio is built
and records the non-obvious decisions, including several that were measured
and settled rather than guessed. Worth reading before a structural change, so
you don't re-litigate something from scratch.
