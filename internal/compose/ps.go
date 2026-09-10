// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package compose

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"
)

// parsePsOutput decodes `docker compose ps --format json`'s output:
// Compose emits one JSON object per line for that flag combination
// (verified against the locally installed `docker compose` — unlike
// most `--format json` Docker CLI subcommands, which emit a single JSON
// array, compose ps emits JSON Lines), so this reads line-by-line rather
// than unmarshaling the whole output as one array.
func parsePsOutput(out string) ([]ServiceState, error) {
	var states []ServiceState
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var s ServiceState
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			return nil, fmt.Errorf("compose: parse ps output line %q: %w", line, err)
		}
		states = append(states, s)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("compose: read ps output: %w", err)
	}
	return states, nil
}
