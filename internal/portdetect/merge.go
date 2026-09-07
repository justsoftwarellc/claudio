package portdetect

import (
	"fmt"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/store"
)

// Manual is one entry from `--ports` on the CLI, e.g. --ports 9229:9229
// parses to Manual{Container: 9229} (the host side is assigned later by
// store.AllocatePort — this package only resolves which container ports
// are mapped and why, never host ports).
type Manual struct {
	ServiceName string // defaults to "manual-<container>" if empty
	Container   int
}

// Resolved is one port to be allocated, with its provenance intact so
// `claudio ports <id>` can explain the mapping (docs/architecture.md
// §6.1). HostPort is filled in later by store.AllocatePort; it is not
// this package's job to allocate.
type Resolved struct {
	ServiceName  string
	Container    int
	Source       store.PortSource
	DetectedFrom *string // nil unless Source == PortDetected
	Expose       bool
}

// Merge combines framework-detected ports, .claudio.yml's declared ports,
// and --ports manual overrides into the final set to allocate, per the
// precedence in docs/architecture.md §6.1: later sources override earlier
// ones on a container-port conflict, and manual supplements rather than
// replaces (a flag naming an already-detected/declared container port
// wins that one mapping only; every other mapping from detection/config
// survives untouched).
func Merge(detected []Detected, declared []config.Port, manual []Manual) ([]Resolved, error) {
	byPort := make(map[int]Resolved)
	var order []int

	add := func(r Resolved) {
		if _, exists := byPort[r.Container]; !exists {
			order = append(order, r.Container)
		}
		byPort[r.Container] = r
	}

	for _, d := range detected {
		from := d.From
		add(Resolved{
			ServiceName:  d.ServiceName,
			Container:    d.Container,
			Source:       store.PortDetected,
			DetectedFrom: &from,
			Expose:       true,
		})
	}

	for _, p := range declared {
		if p.Name == "" {
			return nil, fmt.Errorf("portdetect: declared port %d missing service name", p.Container)
		}
		add(Resolved{
			ServiceName: p.Name,
			Container:   p.Container,
			Source:      store.PortDeclared,
			Expose:      p.Exposed(),
		})
	}

	for _, m := range manual {
		name := m.ServiceName
		if name == "" {
			name = fmt.Sprintf("manual-%d", m.Container)
		}
		add(Resolved{
			ServiceName: name,
			Container:   m.Container,
			Source:      store.PortManual,
			Expose:      true,
		})
	}

	out := make([]Resolved, 0, len(order))
	for _, port := range order {
		out = append(out, byPort[port])
	}
	return out, nil
}
