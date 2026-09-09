package capability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	idPattern      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	namePattern    = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,95}$`)
	releasePattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+$`)
)

func Decode(r io.Reader) (Manifest, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxManifestBytes+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("read capability manifest: %w", err)
	}
	if len(data) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("capability manifest exceeds %d bytes", MaxManifestBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode capability manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Manifest{}, err
	}
	if err := Validate(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("capability manifest contains trailing JSON")
		}
		return fmt.Errorf("decode capability manifest trailer: %w", err)
	}
	return nil
}

func Validate(m Manifest) error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported capability manifest schema version %d; expected %d", m.SchemaVersion, SchemaVersion)
	}
	if err := validateID("capability id", m.ID); err != nil {
		return err
	}
	if err := validateText("display_name", m.DisplayName, 80); err != nil {
		return err
	}
	if err := validateText("summary", m.Summary, 280); err != nil {
		return err
	}
	if !releasePattern.MatchString(m.Release) {
		return fmt.Errorf("release %q must look like v0.1", m.Release)
	}
	if err := validateOwnership(m.Ownership); err != nil {
		return err
	}
	if err := validateTargets(m.Targets); err != nil {
		return err
	}
	if err := validateInputs(m.Inputs); err != nil {
		return err
	}
	if err := validatePrivileges(m.Privileges); err != nil {
		return err
	}
	if err := validateDependencies(m.Dependencies); err != nil {
		return err
	}
	if err := validateConfig(m.Config); err != nil {
		return err
	}
	if err := validateResources(m.Resources); err != nil {
		return err
	}
	if err := validateProvenance(m.Provenance); err != nil {
		return err
	}
	if err := validateHealth(m.Health); err != nil {
		return err
	}
	if err := validateOutputs(m.Outputs); err != nil {
		return err
	}
	if err := validateDeepLinks(m.DeepLinks); err != nil {
		return err
	}
	if err := validateLifecycle(m.Ownership, m.Lifecycle); err != nil {
		return err
	}
	return nil
}

func validateOwnership(values []OwnershipMode) error {
	if len(values) == 0 {
		return fmt.Errorf("ownership must contain at least one mode")
	}
	allowed := map[OwnershipMode]bool{
		OwnershipBuiltin: true, OwnershipExternal: true, OwnershipManagedLocal: true, OwnershipManagedRemote: true,
	}
	seen := map[OwnershipMode]bool{}
	for _, value := range values {
		if !allowed[value] {
			return fmt.Errorf("unknown ownership mode %q", value)
		}
		if seen[value] {
			return fmt.Errorf("duplicate ownership mode %q", value)
		}
		seen[value] = true
	}
	if seen[OwnershipBuiltin] && len(values) != 1 {
		return fmt.Errorf("builtin ownership cannot be combined with managed or external ownership")
	}
	return nil
}

func validateTargets(values []Target) error {
	if len(values) == 0 {
		return fmt.Errorf("targets must contain at least one platform")
	}
	allowedSupport := map[SupportLevel]bool{
		SupportCandidate: true, SupportPlanned: true, SupportTested: true, SupportLimited: true,
	}
	allowedOS := map[string]bool{"darwin": true, "linux": true, "windows": true}
	allowedArch := map[string]bool{"arm64": true, "amd64": true}
	seen := map[string]bool{}
	for _, target := range values {
		if !allowedOS[target.OS] {
			return fmt.Errorf("unsupported target OS %q", target.OS)
		}
		if !allowedArch[target.Arch] {
			return fmt.Errorf("unsupported target architecture %q", target.Arch)
		}
		if !allowedSupport[target.Support] {
			return fmt.Errorf("unknown support level %q", target.Support)
		}
		key := target.OS + "/" + target.Arch
		if seen[key] {
			return fmt.Errorf("duplicate target %s", key)
		}
		seen[key] = true
		if target.MinVersion != "" {
			if _, err := parseVersion(target.MinVersion); err != nil {
				return fmt.Errorf("target %s min_version: %w", key, err)
			}
		}
		if target.Evidence != "" && hasUnsafeText(target.Evidence) {
			return fmt.Errorf("target %s evidence contains control characters", key)
		}
		if target.Support == SupportTested && strings.TrimSpace(target.Evidence) == "" {
			return fmt.Errorf("tested target %s must include evidence", key)
		}
	}
	return nil
}

func validateInputs(values []InputRequirement) error {
	seen := map[string]bool{}
	for _, input := range values {
		if err := validateID("input id", input.ID); err != nil {
			return err
		}
		if seen[input.ID] {
			return fmt.Errorf("duplicate input %q", input.ID)
		}
		seen[input.ID] = true
		if err := validateText("input description", input.Description, 240); err != nil {
			return err
		}
	}
	return nil
}

func validatePrivileges(values []PrivilegeRequirement) error {
	allowed := map[RequirementLevel]bool{RequirementRequired: true, RequirementConditional: true, RequirementNone: true}
	seen := map[string]bool{}
	for _, privilege := range values {
		if err := validateID("privilege id", privilege.ID); err != nil {
			return err
		}
		if seen[privilege.ID] {
			return fmt.Errorf("duplicate privilege %q", privilege.ID)
		}
		seen[privilege.ID] = true
		if !allowed[privilege.Requirement] {
			return fmt.Errorf("unknown privilege requirement %q", privilege.Requirement)
		}
		if err := validateText("privilege description", privilege.Description, 240); err != nil {
			return err
		}
	}
	return nil
}

func validateDependencies(values []Dependency) error {
	allowed := map[DependencyKind]bool{DependencyCapability: true, DependencyRuntime: true, DependencyService: true}
	seen := map[string]bool{}
	for _, dependency := range values {
		if err := validateID("dependency id", dependency.ID); err != nil {
			return err
		}
		key := string(dependency.Kind) + ":" + dependency.ID
		if seen[key] {
			return fmt.Errorf("duplicate dependency %q", key)
		}
		seen[key] = true
		if !allowed[dependency.Kind] {
			return fmt.Errorf("unknown dependency kind %q", dependency.Kind)
		}
		if err := validateText("dependency description", dependency.Description, 240); err != nil {
			return err
		}
		if hasUnsafeText(dependency.VersionConstraint) {
			return fmt.Errorf("dependency version_constraint contains control characters")
		}
	}
	return nil
}

func validateConfig(config ConfigContract) error {
	if config.SchemaVersion <= 0 {
		return fmt.Errorf("config schema_version must be positive")
	}
	seen := map[string]bool{}
	allowed := map[ConfigFieldType]bool{ConfigString: true, ConfigBoolean: true, ConfigInteger: true, ConfigEnum: true, ConfigSecretRef: true}
	for _, field := range config.Fields {
		if !namePattern.MatchString(field.Name) {
			return fmt.Errorf("invalid config field name %q", field.Name)
		}
		if seen[field.Name] {
			return fmt.Errorf("duplicate config field %q", field.Name)
		}
		seen[field.Name] = true
		if !allowed[field.Type] {
			return fmt.Errorf("unknown config field type %q", field.Type)
		}
		if err := validateText("config field description", field.Description, 240); err != nil {
			return err
		}
		if field.Type == ConfigEnum {
			if len(field.Enum) == 0 {
				return fmt.Errorf("enum config field %q must define values", field.Name)
			}
			values := map[string]bool{}
			for _, value := range field.Enum {
				if !namePattern.MatchString(value) {
					return fmt.Errorf("invalid enum value %q for field %q", value, field.Name)
				}
				if values[value] {
					return fmt.Errorf("duplicate enum value %q for field %q", value, field.Name)
				}
				values[value] = true
			}
		} else if len(field.Enum) != 0 {
			return fmt.Errorf("non-enum config field %q cannot define enum values", field.Name)
		}
	}
	return nil
}

func validateResources(resources ResourceBudget) error {
	if !namePattern.MatchString(resources.Profile) {
		return fmt.Errorf("invalid resource profile %q", resources.Profile)
	}
	if resources.Evidence != "" && hasUnsafeText(resources.Evidence) {
		return fmt.Errorf("resource evidence contains control characters")
	}
	switch resources.Measurement {
	case MeasurementUnmeasured:
		if resources.MaxRAMMiB != 0 || resources.MaxDiskMiB != 0 || resources.MaxCPUPercent != 0 {
			return fmt.Errorf("unmeasured resource budget cannot contain measured limits")
		}
	case MeasurementMeasured:
		if resources.MaxRAMMiB <= 0 || resources.MaxDiskMiB <= 0 || resources.MaxCPUPercent <= 0 {
			return fmt.Errorf("measured resource budget requires positive RAM, disk, and CPU limits")
		}
		if strings.TrimSpace(resources.Evidence) == "" {
			return fmt.Errorf("measured resource budget requires evidence")
		}
	default:
		return fmt.Errorf("unknown resource measurement status %q", resources.Measurement)
	}
	return nil
}

func validateProvenance(p Provenance) error {
	if p.Kind != "first-party" && p.Kind != "third-party" {
		return fmt.Errorf("unknown provenance kind %q", p.Kind)
	}
	if err := validateText("provenance license", p.License, 80); err != nil {
		return err
	}
	if err := validateText("provenance source", p.Source, 300); err != nil {
		return err
	}
	if err := validateText("provenance version_policy", p.VersionPolicy, 240); err != nil {
		return err
	}
	return nil
}

func validateHealth(h HealthContract) error {
	if len(h.VerificationSignals) == 0 {
		return fmt.Errorf("health contract must define at least one verification signal")
	}
	seen := map[string]bool{}
	for _, signal := range h.VerificationSignals {
		if err := validateID("verification signal", signal); err != nil {
			return err
		}
		if seen[signal] {
			return fmt.Errorf("duplicate verification signal %q", signal)
		}
		seen[signal] = true
	}
	return nil
}

func validateOutputs(values []OutputContract) error {
	if len(values) == 0 {
		return fmt.Errorf("outputs must contain at least one contract")
	}
	seen := map[string]bool{}
	for _, output := range values {
		if err := validateID("output kind", output.Kind); err != nil {
			return err
		}
		if output.SchemaVersion <= 0 {
			return fmt.Errorf("output %q schema_version must be positive", output.Kind)
		}
		if seen[output.Kind] {
			return fmt.Errorf("duplicate output %q", output.Kind)
		}
		seen[output.Kind] = true
	}
	return nil
}

func validateDeepLinks(values []DeepLink) error {
	seen := map[string]bool{}
	for _, link := range values {
		if err := validateID("deep link id", link.ID); err != nil {
			return err
		}
		if seen[link.ID] {
			return fmt.Errorf("duplicate deep link %q", link.ID)
		}
		seen[link.ID] = true
		contexts := map[string]bool{}
		for _, context := range link.AllowedContexts {
			if err := validateID("deep link context", context); err != nil {
				return err
			}
			if contexts[context] {
				return fmt.Errorf("duplicate deep link context %q", context)
			}
			contexts[context] = true
		}
	}
	return nil
}

func validateLifecycle(ownership []OwnershipMode, actions []LifecycleAction) error {
	if len(actions) == 0 {
		return fmt.Errorf("lifecycle must contain at least one action")
	}
	allowed := map[LifecycleAction]bool{
		ActionPreflight: true, ActionInstall: true, ActionConnect: true, ActionStart: true,
		ActionEnable: true, ActionVerify: true, ActionDisable: true, ActionStop: true,
		ActionDisconnect: true, ActionUpgrade: true, ActionUninstall: true,
	}
	seen := map[LifecycleAction]bool{}
	for _, action := range actions {
		if !allowed[action] {
			return fmt.Errorf("unknown lifecycle action %q", action)
		}
		if seen[action] {
			return fmt.Errorf("duplicate lifecycle action %q", action)
		}
		seen[action] = true
	}
	if containsOwnership(ownership, OwnershipBuiltin) {
		for _, forbidden := range []LifecycleAction{ActionInstall, ActionConnect, ActionStart, ActionStop, ActionDisconnect, ActionUpgrade, ActionUninstall} {
			if seen[forbidden] {
				return fmt.Errorf("builtin capability cannot declare %q lifecycle action", forbidden)
			}
		}
	}
	if !seen[ActionPreflight] || !seen[ActionVerify] {
		return fmt.Errorf("lifecycle must include preflight and verify")
	}
	return nil
}

func containsOwnership(values []OwnershipMode, target OwnershipMode) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validateID(label, value string) error {
	if !idPattern.MatchString(value) {
		return fmt.Errorf("invalid %s %q", label, value)
	}
	return nil
}

func validateText(label, value string, max int) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must be non-empty and trimmed", label)
	}
	if len(value) > max {
		return fmt.Errorf("%s exceeds %d bytes", label, max)
	}
	if hasUnsafeText(value) {
		return fmt.Errorf("%s contains control characters", label)
	}
	return nil
}

func hasUnsafeText(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func parseVersion(value string) ([]int, error) {
	parts := strings.Split(value, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return nil, fmt.Errorf("invalid version %q", value)
	}
	out := make([]int, len(parts))
	for i, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("invalid version %q", value)
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid version %q", value)
		}
		out[i] = n
	}
	return out, nil
}

func compareVersion(a, b []int) int {
	max := len(a)
	if len(b) > max {
		max = len(b)
	}
	for i := 0; i < max; i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}
