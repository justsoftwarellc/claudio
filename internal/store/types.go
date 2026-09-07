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

type Instance struct {
	ID             string
	Name           *string
	RepoURL        string
	RepoRoot       string
	WorktreeDir    string
	Branch         string
	CommitSHA      *string
	Image          string
	ContainerID    *string
	RuntimeProfile string
	DesiredState   DesiredState
	ProvisionStep  ProvisionStep
	CreatedAt      int64
	LastActive     int64
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
	InstanceID    string
	ContainerPort int
	HostPort      int
	Protocol      string
	ServiceName   string
	Source        PortSource
	DetectedFrom  *string
	Status        PortStatus
}
