package store

import (
	"context"
	"errors"
	"fmt"
	"net"
)

var ErrPortRangeExhausted = errors.New("port range exhausted")

// ErrPortNotMapped is returned by ReleasePort when the instance has no
// reservation for the given container port — distinct from a real
// database failure, so a caller (core.RemovePort) can tell "you asked to
// remove something that was never there" from "the store itself broke."
var ErrPortNotMapped = errors.New("store: no such port mapping")

// AllocatePort reserves a free host port in [rangeLow, rangeHigh] for an
// instance's container port, verifying with a real bind() probe before
// committing. The reservation and probe happen inside one transaction so
// two concurrent `claudio create` processes cannot land on the same port —
// see docs/architecture.md §6.2 and §12.4 (CLI-first: N processes share
// this store, not one daemon).
//
// "Free" has two meanings here and they can disagree: unreserved in the
// store, and actually bindable on the host. reserveNextFreePort answers
// only the first — it queries port_mappings and never consults the OS —
// so a port held by a process Claudio has no record of passes the query
// and fails the probe. Ports that fail the probe are therefore
// accumulated in skip and excluded from subsequent attempts (ROD-119).
//
// Without that, the retry could not make progress: a failed probe
// released its reservation, which returned the port to the pool the very
// next query read, so every attempt re-picked the same lowest unbindable
// port and the loop reported the whole range exhausted after burning all
// of its attempts on one port. Two ports held at the bottom of the
// default 43000-43999 range were enough to make every allocation on the
// machine fail with "every host port in 43000-43999 is taken".
func (s *Store) AllocatePort(ctx context.Context, instanceID string, containerPort int, serviceName string, source PortSource, detectedFrom *string, rangeLow, rangeHigh int) (hostPort int, err error) {
	skip := make(map[int]bool)
	for attempt := 0; attempt < (rangeHigh - rangeLow + 1); attempt++ {
		hostPort, err = s.reserveNextFreePort(ctx, instanceID, containerPort, serviceName, source, detectedFrom, rangeLow, rangeHigh, skip)
		if err != nil {
			return 0, err
		}

		if probeBind(hostPort) {
			return hostPort, nil
		}

		// Bind failed: something outside Claudio holds this port. Release the
		// reservation and exclude the port so the next iteration advances to
		// a different one rather than re-picking this same lowest free port.
		skip[hostPort] = true
		if _, delErr := s.db.ExecContext(ctx,
			`DELETE FROM port_mappings WHERE instance_id = ? AND container_port = ?`,
			instanceID, containerPort); delErr != nil {
			return 0, fmt.Errorf("store: release failed probe on port %d: %w", hostPort, delErr)
		}
	}
	return 0, ErrPortRangeExhausted
}

// ReservePort records a specific host port for an instance without a
// bind() probe — for a port that is already published and in use by a
// container Claudio is taking over (core.AdoptContainer), where the
// binding is a fact to record rather than a claim to verify.
//
// AllocatePort is wrong for that case in both directions: its probe
// necessarily fails, because the container being adopted is itself
// listening on the port, and its retry has nowhere to advance to when
// asked for a single-port range. That surfaced as adopt failing with
// "port range exhausted" for a port whose only occupant was the very
// container being adopted (ROD-119).
//
// A UNIQUE(host_port) violation still surfaces as an error: two
// instances must not both claim one host port, and that a container
// already holds it is not a reason to overwrite another instance's
// reservation.
func (s *Store) ReservePort(ctx context.Context, instanceID string, containerPort, hostPort int, serviceName string, source PortSource, detectedFrom *string) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO port_mappings (instance_id, container_port, host_port, protocol, service_name, source, detected_from, status)
		 VALUES (?, ?, ?, 'tcp', ?, ?, ?, 'active')`,
		instanceID, containerPort, hostPort, serviceName, source, detectedFrom); err != nil {
		return fmt.Errorf("store: reserve host port %d for %s: %w", hostPort, instanceID, err)
	}
	return nil
}

// reserveNextFreePort runs the select-then-insert as one BEGIN IMMEDIATE
// transaction. Issuing BEGIN IMMEDIATE as a raw statement (rather than via
// sql.TxOptions, which does not map portably to SQLite's locking modes)
// takes the write lock up front, so a second concurrent caller blocks
// until this transaction commits or rolls back instead of both reading
// the same "free" port and racing on the INSERT.
func (s *Store) reserveNextFreePort(ctx context.Context, instanceID string, containerPort int, serviceName string, source PortSource, detectedFrom *string, rangeLow, rangeHigh int, skip map[int]bool) (port int, err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: acquire connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, fmt.Errorf("store: begin port allocation: %w", err)
	}
	defer func() {
		if err != nil {
			conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	usedSet := make(map[int]bool)
	rows, err := conn.QueryContext(ctx, `SELECT host_port FROM port_mappings WHERE host_port BETWEEN ? AND ?`, rangeLow, rangeHigh)
	if err != nil {
		return 0, fmt.Errorf("store: query reserved ports: %w", err)
	}
	for rows.Next() {
		var p int
		if scanErr := rows.Scan(&p); scanErr != nil {
			rows.Close()
			return 0, fmt.Errorf("store: scan reserved port: %w", scanErr)
		}
		usedSet[p] = true
	}
	if closeErr := rows.Close(); closeErr != nil {
		return 0, closeErr
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return 0, rowsErr
	}

	candidate := 0
	for p := rangeLow; p <= rangeHigh; p++ {
		if !usedSet[p] && !skip[p] {
			candidate = p
			break
		}
	}
	if candidate == 0 {
		return 0, ErrPortRangeExhausted
	}

	if _, err = conn.ExecContext(ctx,
		`INSERT INTO port_mappings (instance_id, container_port, host_port, protocol, service_name, source, detected_from, status)
		 VALUES (?, ?, ?, 'tcp', ?, ?, ?, 'active')`,
		instanceID, containerPort, candidate, serviceName, source, detectedFrom); err != nil {
		return 0, fmt.Errorf("store: reserve port %d: %w", candidate, err)
	}

	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return 0, fmt.Errorf("store: commit port reservation: %w", err)
	}
	return candidate, nil
}

// probeBind verifies a port is actually free by binding to it on 127.0.0.1
// and immediately releasing it. Catches ports held by processes Claudio
// has no record of. See docs/architecture.md §6.2.
func probeBind(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// ReleasePorts frees all port reservations for an instance (stop/destroy).
func (s *Store) ReleasePorts(ctx context.Context, instanceID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM port_mappings WHERE instance_id = ?`, instanceID)
	if err != nil {
		return fmt.Errorf("store: release ports for %s: %w", instanceID, err)
	}
	return nil
}

// ReleasePort frees a single port reservation — the mechanism behind
// `claudio ports <id> --remove <container>` (ROD-98). Unlike ReleasePorts,
// this only ever touches the store: removing a mapping from an already-
// running container's published ports is not possible without recreating
// it (Docker cannot drop a binding from a running container, the same
// asymmetry that makes --add require a restart), so the caller is
// expected to warn the user that the change takes effect on the next
// `claudio restart`, not immediately.
func (s *Store) ReleasePort(ctx context.Context, instanceID string, containerPort int) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM port_mappings WHERE instance_id = ? AND container_port = ?`, instanceID, containerPort)
	if err != nil {
		return fmt.Errorf("store: release port %d for %s: %w", containerPort, instanceID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: release port %d for %s: %w", containerPort, instanceID, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: instance %s, container port %d", ErrPortNotMapped, instanceID, containerPort)
	}
	return nil
}

func (s *Store) PortMappings(ctx context.Context, instanceID string) ([]PortMapping, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT instance_id, container_port, host_port, protocol, service_name, source, detected_from, status
		 FROM port_mappings WHERE instance_id = ? ORDER BY container_port`, instanceID)
	if err != nil {
		return nil, fmt.Errorf("store: query port mappings: %w", err)
	}
	defer rows.Close()

	var out []PortMapping
	for rows.Next() {
		var m PortMapping
		if err := rows.Scan(&m.InstanceID, &m.ContainerPort, &m.HostPort, &m.Protocol, &m.ServiceName, &m.Source, &m.DetectedFrom, &m.Status); err != nil {
			return nil, fmt.Errorf("store: scan port mapping: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
