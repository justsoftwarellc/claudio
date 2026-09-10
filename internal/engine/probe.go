// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

// Package engine detects and talks to the container runtime. See
// docs/architecture.md §12.1: the engine's identity changes the correct
// mount strategy, and OrbStack is the target runtime for this project —
// `docker info`, never `docker --version`, is the check that distinguishes
// it, because the latter reports the client, not the server.
package engine

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/docker/docker/client"
)

type RuntimeProfile string

const (
	ProfileOrbStack      RuntimeProfile = "orbstack"
	ProfileDockerDesktop RuntimeProfile = "docker-desktop"
	ProfileNative        RuntimeProfile = "native"
	ProfileGeneric       RuntimeProfile = "generic"
)

// Info is what DetectRuntime learns from one call to the Docker API.
type Info struct {
	Profile         RuntimeProfile
	OperatingSystem string
	ServerVersion   string
	MemTotal        int64
	NCPU            int
	DockerHost      string // the endpoint actually used, for diagnostics
}

// DetectRuntime probes the Docker Engine's /info endpoint and classifies
// the result into a RuntimeProfile. This must be a live API call, not a
// version string: `docker --version` reports the CLI client, and on a
// machine with multiple Docker contexts configured that reveals nothing
// about which daemon containers actually run on.
//
// host, if non-empty, pins the endpoint (from global config's
// runtime.docker_host — see ROD-113). Otherwise the resolution order is:
// DOCKER_HOST env var, then the docker CLI's *current context* (which is
// what `docker` itself honors via ~/.docker/config.json), then the
// client SDK's own default.
//
// The context step matters and was found empirically on this machine: the
// OS-level default socket /var/run/docker.sock was symlinked to Docker
// Desktop's socket even though `docker info` (and every `docker` command)
// correctly used OrbStack, because the CLI reads currentContext from
// ~/.docker/config.json and client.FromEnv does not. Skipping this step
// silently classifies the runtime as docker-desktop when the user, and
// every `docker` command they run, is actually on OrbStack.
func DetectRuntime(ctx context.Context, host string) (Info, error) {
	resolvedHost := host
	if resolvedHost == "" {
		resolvedHost = currentContextHost(ctx)
	}

	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if resolvedHost != "" {
		opts = append(opts, client.WithHost(resolvedHost))
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return Info{}, fmt.Errorf("engine: create docker client: %w", err)
	}
	defer cli.Close()

	sysInfo, err := cli.Info(ctx)
	if err != nil {
		return Info{}, fmt.Errorf("engine: docker info: %w (is the Docker/OrbStack daemon running?)", err)
	}

	info := Info{
		OperatingSystem: sysInfo.OperatingSystem,
		ServerVersion:   sysInfo.ServerVersion,
		MemTotal:        sysInfo.MemTotal,
		NCPU:            sysInfo.NCPU,
		DockerHost:      resolvedHost,
	}
	info.Profile = classify(sysInfo.OperatingSystem, runtime.GOOS)
	return info, nil
}

// currentContextHost asks the docker CLI which endpoint its current
// context resolves to. Deliberately shells out rather than hand-parsing
// ~/.docker/contexts/meta/<hash>/meta.json: that layout is undocumented
// and hashed by context name, and reimplementing Docker's own
// context-resolution logic (config.json currentContext, DOCKER_CONTEXT
// env override, endpoint TLS options) would be reinventing what the
// installed `docker` binary already does correctly. Returns "" on any
// failure — including "docker" not being on PATH — so DetectRuntime falls
// through to the SDK's own default resolution.
func currentContextHost(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "docker", "context", "inspect",
		"--format", `{{(index .Endpoints "docker").Host}}`).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// classify maps a daemon's reported OperatingSystem string to a runtime
// profile. VM-backed runtimes self-report identifiably ("OrbStack",
// "Docker Desktop"); native Linux Docker reports the *distro name*
// (Ubuntu, Debian, Alpine, ...), which has no reliable common substring —
// Ubuntu's own string does not contain "linux". The only trustworthy
// signal for "native" is therefore not the OS string at all: it is that
// the CLI process itself is running on GOOS=linux with neither VM marker
// present. On darwin/windows, an unrecognized OperatingSystem string means
// an unrecognized VM-backed runtime, not "native" — hence generic.
func classify(operatingSystem, hostGOOS string) RuntimeProfile {
	os := strings.ToLower(operatingSystem)
	switch {
	case strings.Contains(os, "orbstack"):
		return ProfileOrbStack
	case strings.Contains(os, "docker desktop"):
		return ProfileDockerDesktop
	case hostGOOS == "linux":
		return ProfileNative
	default:
		return ProfileGeneric
	}
}
