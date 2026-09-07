package engine

// Label keys stamped on every container Claudio creates. Everything
// observable about a container is read live via these labels rather than
// duplicated into SQLite (docs/architecture.md §10.1) — this is also what
// makes an instance fully recoverable from Docker alone if state.db is
// lost, and what lets `claudio adopt` reconstruct a row from a container
// that outlived its database entry (ROD-99).
const (
	LabelInstanceID = "claudio.instance.id"
	LabelRepo       = "claudio.repo"
	LabelCreatedAt  = "claudio.created_at"
)
