// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

// Package image embeds the base Dockerfile and entrypoint script into the
// claudio binary itself (ROD-96). `claudio image build` must work from a
// binary run anywhere on the host — a Homebrew install, a copied binary,
// a CI runner — none of which can be assumed to sit next to a checked-out
// image/ directory the way running `go run ./cmd/claudio` from a repo
// clone does. go:embed makes the binary self-contained: the build context
// is assembled in memory from these bytes rather than read off disk at
// image-build time.
package image

import _ "embed"

//go:embed Dockerfile
var Dockerfile []byte

//go:embed entrypoint.sh
var Entrypoint []byte

// ClaudeJSONTemplate is the onboarding pre-seed the entrypoint merges
// into ~/.claude.json at boot. A real file COPYed from the build
// context, not a Dockerfile heredoc: heredocs are BuildKit-only, and
// the SDK build path this package uses selects the legacy builder, where
// one yields an empty file (see image/Dockerfile for the measurements).
//
//go:embed claude.json.template
var ClaudeJSONTemplate []byte
