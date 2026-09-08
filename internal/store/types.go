package store

// DesiredState is what the user asked for — never what Docker reports.
// Observed container state is read live and merged at query time; storing
// it here is what creates drift. See docs/architecture.md §10.1.
type DesiredState string

const (
	StateRunning   DesiredState = "running"
	StateStopped   DesiredState = "stopped"
	StateDestroyed DesiredState = "destroyed"
)

// ProvisionStep is the resume point for the provisioning state machine
// (docs/architecture.md §4.1). A crashed process picks up from here rather
// than restarting from scratch.
type ProvisionStep string

const (
	StepPending     ProvisionStep = "pending"
	StepRepoReady   ProvisionStep = "repo_ready"   // root cloned/verified, worktree added
	StepPortsReady  ProvisionStep = "ports_ready"  // detection + allocation done
	StepConfigReady ProvisionStep = "config_ready" // resolved.yml materialized
	StepContainerUp ProvisionStep = "container_up" // container created and started
	StepHealthy     ProvisionStep = "healthy"      // health probe passed; terminal
	StepFailed      ProvisionStep = "failed"
)

// Instance is tagged for JSON per docs/architecture.md §12.4's interface
// discipline (phase 2's HTTP layer serializes this verbatim), using
// snake_case to match the SQLite column names and the rest of the wire
// surface (config.yml is snake_case throughout — see internal/config's
// package doc).
type Instance struct {
	ID             string        `json:"id"`
	Name           *string       `json:"name,omitempty"`
	RepoURL        string        `json:"repo_url"`
	RepoRoot       string        `json:"repo_root"`
	WorktreeDir    string        `json:"worktree_dir"`
	Branch         string        `json:"branch"`
	CommitSHA      *string       `json:"commit_sha,omitempty"`
	Image          string        `json:"image"`
	ContainerID    *string       `json:"container_id,omitempty"`
	RuntimeProfile string        `json:"runtime_profile"`
	DesiredState   DesiredState  `json:"desired_state"`
	ProvisionStep  ProvisionStep `json:"provision_step"`
	CreatedAt      int64         `json:"created_at"`
	LastActive     int64         `json:"last_active"`
}

type PortSource string

const (
	PortDetected PortSource = "detected"
	PortDeclared PortSource = "declared"
	PortManual   PortSource = "manual"
)

type PortStatus string

const (
	PortActive PortStatus = "active"
	PortStale  PortStatus = "stale" // listener disappeared; mapping retained (ROD-101)
)

type PortMapping struct {
	InstanceID    string     `json:"instance_id"`
	ContainerPort int        `json:"container_port"`
	HostPort      int        `json:"host_port"`
	Protocol      string     `json:"protocol"`
	ServiceName   string     `json:"service_name"`
	Source        PortSource `json:"source"`
	DetectedFrom  *string    `json:"detected_from,omitempty"`
	Status        PortStatus `json:"status"`
}
