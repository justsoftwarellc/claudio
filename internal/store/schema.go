package store

// Migrations are applied in order, once each, tracked in schema_version.
// Never edit an applied migration — append a new one instead.
var migrations = []string{
	migration001,
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
