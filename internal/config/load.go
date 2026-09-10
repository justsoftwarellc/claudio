// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadRepoConfig reads and strictly decodes <repo>/.claudio.yml. A missing
// file is not an error — every key is optional and absence just means "use
// the resolved defaults" (ROD-113). An unknown key IS an error: a typo in
// post_create should say so rather than silently doing nothing.
func LoadRepoConfig(path string) (RepoConfig, error) {
	var cfg RepoConfig
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := decodeStrict(data, &cfg); err != nil {
		return cfg, fmt.Errorf("config: parse %s: %w", path, err)
	}
	for i, p := range cfg.Ports {
		if p.Name == "" {
			return cfg, fmt.Errorf("config: %s: ports[%d] missing required field 'name'", path, i)
		}
		if p.Container == 0 {
			return cfg, fmt.Errorf("config: %s: ports[%d] (%s) missing required field 'container'", path, i, p.Name)
		}
	}
	seenHostService := make(map[string]bool, len(cfg.HostServices))
	for i, hs := range cfg.HostServices {
		if hs.Name == "" {
			return cfg, fmt.Errorf("config: %s: host_services[%d] missing required field 'name'", path, i)
		}
		if hs.Host == 0 {
			return cfg, fmt.Errorf("config: %s: host_services[%d] (%s) missing required field 'host'", path, i, hs.Name)
		}
		// The name becomes a hostname inside the container, so a duplicate
		// is not a harmless repeat: two /etc/hosts entries for one name
		// resolve to whichever Docker wrote first, silently ignoring the
		// second declaration. Rejecting it here is the only place the user
		// can still be told which name collided.
		if seenHostService[hs.Name] {
			return cfg, fmt.Errorf("config: %s: host_services[%d]: duplicate name %q — each name becomes one hostname inside the container", path, i, hs.Name)
		}
		seenHostService[hs.Name] = true
		if err := validatePort(hs.Host); err != nil {
			return cfg, fmt.Errorf("config: %s: host_services[%d] (%s): host: %w", path, i, hs.Name, err)
		}
		if hs.Container != nil {
			if err := validatePort(*hs.Container); err != nil {
				return cfg, fmt.Errorf("config: %s: host_services[%d] (%s): container: %w", path, i, hs.Name, err)
			}
		}
	}
	for i, svc := range cfg.Services {
		if svc.Image == "" {
			return cfg, fmt.Errorf("config: %s: services[%d] missing required field 'image'", path, i)
		}
	}
	return cfg, nil
}

// LoadGlobalConfig reads ~/.claudio/config.yml, or Defaults() if absent.
func LoadGlobalConfig(path string) (GlobalConfig, error) {
	cfg := Defaults()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("config: read %s: %w", path, err)
	}

	var fromFile GlobalConfig
	if err := decodeStrict(data, &fromFile); err != nil {
		return cfg, fmt.Errorf("config: parse %s: %w", path, err)
	}

	if fromFile.WorkspaceRoot != "" {
		cfg.WorkspaceRoot = fromFile.WorkspaceRoot
	}
	cfg.Resources = cfg.Resources.Merge(fromFile.Resources)
	if fromFile.Ports.Range != [2]int{} {
		cfg.Ports.Range = fromFile.Ports.Range
	}
	if fromFile.Ports.Bind != "" {
		cfg.Ports.Bind = fromFile.Ports.Bind
	}
	if fromFile.Runtime.DockerHost != "" {
		cfg.Runtime.DockerHost = fromFile.Runtime.DockerHost
	}
	return cfg, nil
}

// decodeStrict rejects unknown keys — see the package doc: a config
// mistake should be a decode error, not a silent no-op.
func decodeStrict(data []byte, out interface{}) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(out)
}

// validatePort rejects a port number outside TCP's actual range. Checked
// at load rather than at container-create time so a typo in .claudio.yml
// names the file and the entry, instead of surfacing later as a Docker
// error about an address it was handed.
func validatePort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("%d is not a valid TCP port (1-65535)", p)
	}
	return nil
}
