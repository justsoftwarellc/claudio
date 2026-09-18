// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package store

// Migrations are applied in order, once each, tracked in schema_version.
// Never edit an applied migration — append a new one instead.
var migrations = []string{
	migration001,
	migration002,
	migration003,
}

const migration001 = `
CREATE TABLE schema_version (
	version    INTEGER NOT NULL,
	applied_at INTEGER NOT NULL
);

CREATE TABLE instances (
	id              TEXT PRIMARY KEY,
	name            TEXT UNIQUE,
	repo_url        TEXT NOT NULL,
	repo_root       TEXT NOT NULL,
	worktree_dir    TEXT NOT NULL,
	branch          TEXT NOT NULL,
	commit_sha      TEXT,
	image           TEXT NOT NULL,
	container_id    TEXT,
	runtime_profile TEXT NOT NULL,
	desired_state   TEXT NOT NULL DEFAULT 'running',
	provision_step  TEXT NOT NULL DEFAULT 'pending',
	created_at      INTEGER NOT NULL,
	last_active     INTEGER NOT NULL
);

CREATE UNIQUE INDEX idx_instances_name ON instances(name) WHERE name IS NOT NULL;

CREATE TABLE port_mappings (
	instance_id    TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
	container_port INTEGER NOT NULL,
	host_port      INTEGER NOT NULL,
	protocol       TEXT NOT NULL DEFAULT 'tcp',
	service_name   TEXT NOT NULL,
	source         TEXT NOT NULL,
	detected_from  TEXT,
	status         TEXT NOT NULL DEFAULT 'active',
	PRIMARY KEY (instance_id, container_port),
	UNIQUE (host_port)
);

CREATE TABLE repos (
	root_path     TEXT PRIMARY KEY,
	repo_url      TEXT NOT NULL,
	last_fetch_at INTEGER
);

CREATE TABLE events (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	instance_id TEXT REFERENCES instances(id) ON DELETE CASCADE,
	kind        TEXT NOT NULL,
	message     TEXT NOT NULL,
	created_at  INTEGER NOT NULL
);

CREATE INDEX idx_events_instance ON events(instance_id, created_at);
`

// migration002 adds compose-project tracking (ROD-106): an instance
// backed by a docker-compose.yml (or synthesized .claudio.yml services:)
// is a compose *project*, not a single container, and every lifecycle
// operation (stop/start/restart/destroy) needs to know that to run
// `docker compose` rather than a single container remove/create. NULL
// means "ordinary single-container instance" — the common case, and
// every row created before this migration.
const migration002 = `
ALTER TABLE instances ADD COLUMN compose_project TEXT;
`

// migration003 records where a repo's local .claudio.yml lives (ROD-133).
//
// The path is knowable today only by string-parsing repo_url's
// "file://<path>" form, which is why two commands could read two
// different files and neither could say which was authoritative. Config
// is local, and the store is what knows where it is: an explicit column
// makes that a fact to look up rather than a convention to re-derive.
//
// NULL means "no source directory" — an instance created from a remote
// URL, which has no folder the user stands in — and is what the resolver
// branches on to fall back to the worktree. Distinct from the empty
// string, and the reason this is a nullable column rather than a
// defaulted one.
const migration003 = `
ALTER TABLE repos ADD COLUMN source_dir TEXT;
`
