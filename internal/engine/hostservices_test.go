// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package engine

import (
	"strings"
	"testing"
)

// The gateway alias is added even with no declared services: it is what
// makes host.docker.internal mean the same thing on plain Linux Docker
// as it already does on OrbStack/Docker Desktop, where it resolves via
// embedded DNS and is absent from /etc/hosts entirely.
func TestExtraHostsAlwaysIncludesGatewayAlias(t *testing.T) {
	got := ExtraHosts(nil)
	want := HostGatewayAlias + ":host-gateway"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("ExtraHosts(nil) = %v, want exactly [%s]", got, want)
	}
}

func TestExtraHostsMapsEachServiceToTheGateway(t *testing.T) {
	got := ExtraHosts([]HostService{
		{Name: "db", HostPort: 5432, ContainerPort: 5432},
		{Name: "cache", HostPort: 6379, ContainerPort: 6379},
	})

	want := []string{
		HostGatewayAlias + ":host-gateway",
		"db:host-gateway",
		"cache:host-gateway",
	}
	if len(got) != len(want) {
		t.Fatalf("ExtraHosts returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Nothing but the declared names may appear: an undeclared name must not
// resolve inside the container, which is the whole basis for calling this
// opt-in rather than a blanket route to the host (§7.4).
func TestExtraHostsAddsNothingUndeclared(t *testing.T) {
	got := ExtraHosts([]HostService{{Name: "db", HostPort: 5432, ContainerPort: 5432}})
	for _, entry := range got {
		name, _, _ := strings.Cut(entry, ":")
		if name != HostGatewayAlias && name != "db" {
			t.Errorf("ExtraHosts produced an undeclared entry %q", entry)
		}
	}
}

func TestValidateHostServices(t *testing.T) {
	tests := []struct {
		name     string
		services []HostService
		wantErr  string
	}{
		{name: "empty is valid", services: nil},
		{
			name:     "distinct names",
			services: []HostService{{Name: "db", HostPort: 5432}, {Name: "cache", HostPort: 6379}},
		},
		{
			name:     "container port equal to host is fine",
			services: []HostService{{Name: "db", HostPort: 5432, ContainerPort: 5432}},
		},
		{
			// A hosts entry maps a name to an address; there is no port
			// component, so a differing container port cannot be honored.
			// Refusing beats resolving the name to a host serving nothing.
			name:     "differing container port is refused",
			services: []HostService{{Name: "db", HostPort: 5432, ContainerPort: 6000}},
			wantErr:  "not to a port",
		},
		{
			name:     "duplicate names",
			services: []HostService{{Name: "db", HostPort: 5432}, {Name: "db", HostPort: 6379}},
			wantErr:  "duplicate host service name",
		},
		{
			name:     "shadowing the built-in alias",
			services: []HostService{{Name: HostGatewayAlias, HostPort: 5432}},
			wantErr:  "shadows the built-in host alias",
		},
		{
			name:     "missing name",
			services: []HostService{{HostPort: 5432}},
			wantErr:  "no name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateHostServices(tc.services)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateHostServices(%+v) unexpected error: %v", tc.services, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateHostServices(%+v) = nil, want error containing %q", tc.services, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
