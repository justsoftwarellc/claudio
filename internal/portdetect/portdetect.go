// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

// Package portdetect infers which container ports a repo's app listens on
// from manifest files, per docs/architecture.md §6.1 and ROD-98's phase-1
// scope: framework defaults only. Richer signals (docker-compose.yml,
// package.json scripts, .env) are phase 2 (ROD-101); compose-based repos
// are handled separately as sidecar projects (ROD-106), not through this
// package.
package portdetect

import (
	"os"
	"path/filepath"
)

// Detected is one framework-default guess: a container port with the
// manifest signal that produced it, before any merging with declared or
// manual sources (see Merge).
type Detected struct {
	ServiceName string
	Container   int
	From        string // e.g. "next.config.js" — surfaced in `claudio ports <id>`
}

// signal is one row of the docs/architecture.md §6.1 framework-defaults
// table. match is called with the repo directory and reports whether this
// signal fires, plus the manifest filename actually found (so From can
// name "next.config.js" rather than a family like "next.config.*").
type signal struct {
	service string
	port    int
	match   func(dir string) (matched bool, file string)
}

var signals = []signal{
	{"web", 3000, globMatch("next.config.js", "next.config.mjs", "next.config.ts")},
	{"web", 5173, globMatch("vite.config.js", "vite.config.mjs", "vite.config.ts")},
	{"web", 3000, packageJSONHasDependency("react-scripts")},
	{"web", 8000, globMatch("manage.py")},
	{"web", 3000, railsMatch},
	{"api", 8080, goModMatch},
}

// Detect scans dir (a repo root or worktree) and returns every framework
// signal that fires, in table order. Multiple signals can fire on the same
// repo (e.g. a Go API alongside a Vite frontend); callers do not need to
// pick just one.
func Detect(dir string) ([]Detected, error) {
	var out []Detected
	for _, s := range signals {
		matched, file := s.match(dir)
		if !matched {
			continue
		}
		out = append(out, Detected{ServiceName: s.service, Container: s.port, From: file})
	}
	return out, nil
}

func globMatch(names ...string) func(dir string) (bool, string) {
	return func(dir string) (bool, string) {
		for _, name := range names {
			if fileExists(filepath.Join(dir, name)) {
				return true, name
			}
		}
		return false, ""
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func packageJSONHasDependency(dep string) func(dir string) (bool, string) {
	return func(dir string) (bool, string) {
		path := filepath.Join(dir, "package.json")
		data, err := os.ReadFile(path)
		if err != nil {
			return false, ""
		}
		if containsDependencyKey(data, dep) {
			return true, "package.json"
		}
		return false, ""
	}
}

func railsMatch(dir string) (bool, string) {
	if fileExists(filepath.Join(dir, "Gemfile")) && fileExists(filepath.Join(dir, "config.ru")) {
		return true, "Gemfile"
	}
	return false, ""
}

func goModMatch(dir string) (bool, string) {
	if fileExists(filepath.Join(dir, "go.mod")) {
		return true, "go.mod"
	}
	return false, ""
}
