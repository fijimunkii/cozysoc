package capability

const SchemaVersion = 1

const MaxManifestBytes = 64 * 1024

type OwnershipMode string

const (
	OwnershipBuiltin       OwnershipMode = "builtin"
	OwnershipExternal      OwnershipMode = "external"
	OwnershipManagedLocal  OwnershipMode = "managed-local"
	OwnershipManagedRemote OwnershipMode = "managed-remote"
)

type SupportLevel string

const (
	SupportCandidate SupportLevel = "candidate"
	SupportPlanned   SupportLevel = "planned"
	SupportTested    SupportLevel = "tested"
	SupportLimited   SupportLevel = "limited"
)

type RequirementLevel string

const (
	RequirementRequired    RequirementLevel = "required"
	RequirementConditional RequirementLevel = "conditional"
	RequirementNone        RequirementLevel = "none"
)

type DependencyKind string

const (
	DependencyCapability DependencyKind = "capability"
	DependencyRuntime    DependencyKind = "runtime"
	DependencyService    DependencyKind = "service"
)

type ConfigFieldType string

const (
	ConfigString    ConfigFieldType = "string"
	ConfigBoolean   ConfigFieldType = "boolean"
	ConfigInteger   ConfigFieldType = "integer"
	ConfigEnum      ConfigFieldType = "enum"
	ConfigSecretRef ConfigFieldType = "secret-ref"
)

type MeasurementStatus string

const (
	MeasurementUnmeasured MeasurementStatus = "unmeasured"
	MeasurementMeasured   MeasurementStatus = "measured"
)

type LifecycleAction string

const (
	ActionPreflight  LifecycleAction = "preflight"
	ActionInstall    LifecycleAction = "install"
	ActionConnect    LifecycleAction = "connect"
	ActionStart      LifecycleAction = "start"
	ActionEnable     LifecycleAction = "enable"
	ActionVerify     LifecycleAction = "verify"
	ActionDisable    LifecycleAction = "disable"
	ActionStop       LifecycleAction = "stop"
	ActionDisconnect LifecycleAction = "disconnect"
	ActionUpgrade    LifecycleAction = "upgrade"
	ActionUninstall  LifecycleAction = "uninstall"
)

type Manifest struct {
	SchemaVersion int                    `json:"schema_version"`
	ID            string                 `json:"id"`
	DisplayName   string                 `json:"display_name"`
	Summary       string                 `json:"summary"`
	Release       string                 `json:"release"`
	Ownership     []OwnershipMode        `json:"ownership"`
	Targets       []Target               `json:"targets"`
	Inputs        []InputRequirement     `json:"inputs"`
	Privileges    []PrivilegeRequirement `json:"privileges"`
	Dependencies  []Dependency           `json:"dependencies"`
	Config        ConfigContract         `json:"config"`
	Resources     ResourceBudget         `json:"resources"`
	Provenance    Provenance             `json:"provenance"`
	Health        HealthContract         `json:"health"`
	Outputs       []OutputContract       `json:"outputs"`
	DeepLinks     []DeepLink             `json:"deep_links"`
	Lifecycle     []LifecycleAction      `json:"lifecycle"`
}

type Target struct {
	OS         string       `json:"os"`
	Arch       string       `json:"arch"`
	MinVersion string       `json:"min_version,omitempty"`
	Support    SupportLevel `json:"support"`
	Evidence   string       `json:"evidence,omitempty"`
}

type InputRequirement struct {
	ID          string `json:"id"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

type PrivilegeRequirement struct {
	ID          string           `json:"id"`
	Requirement RequirementLevel `json:"requirement"`
	Description string           `json:"description"`
}

type Dependency struct {
	ID                string         `json:"id"`
	Kind              DependencyKind `json:"kind"`
	Required          bool           `json:"required"`
	VersionConstraint string         `json:"version_constraint,omitempty"`
	Description       string         `json:"description"`
}

type ConfigContract struct {
	SchemaVersion int           `json:"schema_version"`
	Fields        []ConfigField `json:"fields"`
}

type ConfigField struct {
	Name        string          `json:"name"`
	Type        ConfigFieldType `json:"type"`
	Required    bool            `json:"required"`
	Description string          `json:"description"`
	Enum        []string        `json:"enum,omitempty"`
}

type ResourceBudget struct {
	Measurement   MeasurementStatus `json:"measurement"`
	Profile       string            `json:"profile"`
	MaxRAMMiB     int               `json:"max_ram_mib,omitempty"`
	MaxDiskMiB    int               `json:"max_disk_mib,omitempty"`
	MaxCPUPercent float64           `json:"max_cpu_percent,omitempty"`
	Evidence      string            `json:"evidence,omitempty"`
}

type Provenance struct {
	Kind          string `json:"kind"`
	License       string `json:"license"`
	Source        string `json:"source"`
	VersionPolicy string `json:"version_policy"`
}

type HealthContract struct {
	ProcessRequired              bool     `json:"process_required"`
	VerificationSignals          []string `json:"verification_signals"`
	CoverageRequiresVerification bool     `json:"coverage_requires_verification"`
}

type OutputContract struct {
	Kind          string `json:"kind"`
	SchemaVersion int    `json:"schema_version"`
}

type DeepLink struct {
	ID              string   `json:"id"`
	AllowedContexts []string `json:"allowed_contexts"`
}

func cloneManifest(m Manifest) Manifest {
	out := m
	out.Ownership = append([]OwnershipMode(nil), m.Ownership...)
	out.Targets = append([]Target(nil), m.Targets...)
	out.Inputs = append([]InputRequirement(nil), m.Inputs...)
	out.Privileges = append([]PrivilegeRequirement(nil), m.Privileges...)
	out.Dependencies = append([]Dependency(nil), m.Dependencies...)
	out.Config.Fields = make([]ConfigField, len(m.Config.Fields))
	for i, field := range m.Config.Fields {
		out.Config.Fields[i] = field
		out.Config.Fields[i].Enum = append([]string(nil), field.Enum...)
	}
	out.Health.VerificationSignals = append([]string(nil), m.Health.VerificationSignals...)
	out.Outputs = append([]OutputContract(nil), m.Outputs...)
	out.DeepLinks = make([]DeepLink, len(m.DeepLinks))
	for i, link := range m.DeepLinks {
		out.DeepLinks[i] = link
		out.DeepLinks[i].AllowedContexts = append([]string(nil), link.AllowedContexts...)
	}
	out.Lifecycle = append([]LifecycleAction(nil), m.Lifecycle...)
	return out
}
