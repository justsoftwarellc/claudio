// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

// Package idgen generates short, human-typeable instance identifiers —
// "brave-otter", never a hash — per docs/architecture.md §5.2/§9's
// identity model: the generated ID is permanent and canonical, used
// verbatim in the default branch name (claudio/<id>) and the container
// name (claudio-<id>).
package idgen

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// adjectives and nouns are deliberately short, unambiguous words: no two
// entries share a common prefix that would confuse the unambiguous-prefix
// matching docs/architecture.md §9 promises for commands like `claudio
// attach brave`.
var adjectives = []string{
	"brave", "calm", "eager", "fuzzy", "gentle", "happy", "jolly", "keen",
	"lively", "misty", "noble", "proud", "quiet", "rapid", "sunny", "swift",
	"tidy", "vivid", "witty", "zesty",
}

var nouns = []string{
	"otter", "falcon", "badger", "heron", "lynx", "marlin", "panther",
	"raven", "salmon", "tiger", "viper", "walrus", "yak", "zebra",
	"beetle", "condor", "dingo", "egret", "ferret", "gecko",
}

// New returns a random "<adjective>-<noun>" candidate. Callers are
// expected to retry on a collision (the store's UNIQUE(id) constraint is
// the source of truth — see core.CreateInstance) rather than have this
// package guarantee uniqueness itself, since only the caller can check
// the store.
func New() (string, error) {
	adj, err := pick(adjectives)
	if err != nil {
		return "", err
	}
	noun, err := pick(nouns)
	if err != nil {
		return "", err
	}
	return adj + "-" + noun, nil
}

func pick(words []string) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
	if err != nil {
		return "", fmt.Errorf("idgen: %w", err)
	}
	return words[n.Int64()], nil
}
