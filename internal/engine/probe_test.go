// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package engine

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		name     string
		os       string
		hostGOOS string
		want     RuntimeProfile
	}{
		{"orbstack on darwin", "OrbStack", "darwin", ProfileOrbStack},
		{"orbstack lowercase", "orbstack", "darwin", ProfileOrbStack},
		{"docker desktop on darwin", "Docker Desktop", "darwin", ProfileDockerDesktop},
		{"docker desktop on windows", "Docker Desktop", "windows", ProfileDockerDesktop},
		// Native Linux daemons report the distro name, not the word
		// "linux" — Ubuntu's own OperatingSystem string has no such
		// substring. The host's own GOOS is the only reliable signal.
		{"ubuntu on linux host", "Ubuntu 22.04.3 LTS", "linux", ProfileNative},
		{"alpine on linux host", "Alpine Linux v3.19", "linux", ProfileNative},
		{"debian on linux host", "Debian GNU/Linux 12 (bookworm)", "linux", ProfileNative},
		// Same OS string, but the CLI process isn't on Linux: this is an
		// unrecognized VM-backed runtime, not native.
		{"linux-reporting daemon reached from darwin", "Ubuntu 22.04.3 LTS", "darwin", ProfileGeneric},
		{"unknown on darwin", "", "darwin", ProfileGeneric},
		{"unknown on windows", "Windows Server 2022", "windows", ProfileGeneric},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.os, c.hostGOOS); got != c.want {
				t.Errorf("classify(%q, %q) = %q, want %q", c.os, c.hostGOOS, got, c.want)
			}
		})
	}
}
