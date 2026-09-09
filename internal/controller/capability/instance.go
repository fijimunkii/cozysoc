package capability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

const MaxInstanceConfigBytes = 32 * 1024

type Configuration struct {
	ID        string                     `json:"id"`
	Ownership OwnershipMode              `json:"ownership"`
	Desired   DesiredState               `json:"desired"`
	Values    map[string]json.RawMessage `json:"values,omitempty"`
}

type Instance struct {
	Manifest   Manifest      `json:"manifest"`
	Configured bool          `json:"configured"`
	Ownership  OwnershipMode `json:"ownership"`
	State      InstanceState `json:"state"`
}

type Instances struct {
	registry   *Registry
	configured map[string]Configuration
}

func NewInstances(registry *Registry, configured []Configuration) (*Instances, error) {
	if registry == nil {
		return nil, fmt.Errorf("capability registry is required")
	}
	instances := &Instances{
		registry:   registry,
		configured: make(map[string]Configuration, len(configured)),
	}
	for _, configuration := range configured {
		if _, exists := instances.configured[configuration.ID]; exists {
			return nil, fmt.Errorf("duplicate capability configuration %q", configuration.ID)
		}
		if err := ValidateConfiguration(registry, configuration); err != nil {
			return nil, fmt.Errorf("capability configuration %q: %w", configuration.ID, err)
		}
		instances.configured[configuration.ID] = cloneConfiguration(configuration)
	}
	return instances, nil
}

func (i *Instances) List() []Instance {
	if i == nil || i.registry == nil {
		return nil
	}
	manifests := i.registry.List()
	out := make([]Instance, 0, len(manifests))
	for _, manifest := range manifests {
		out = append(out, i.instanceFor(manifest))
	}
	return out
}

func (i *Instances) Get(id string) (Instance, bool) {
	if i == nil || i.registry == nil {
		return Instance{}, false
	}
	manifest, ok := i.registry.Get(id)
	if !ok {
		return Instance{}, false
	}
	return i.instanceFor(manifest), true
}

func (i *Instances) instanceFor(manifest Manifest) Instance {
	configuration, explicit := i.configured[manifest.ID]
	if !explicit {
		configuration = defaultConfiguration(manifest)
	}
	process := ProcessNotApplicable
	if manifest.Health.ProcessRequired {
		process = ProcessStopped
	}
	return Instance{
		Manifest:   manifest,
		Configured: explicit,
		Ownership:  configuration.Ownership,
		State: InstanceState{
			Desired:      configuration.Desired,
			Process:      process,
			Verification: VerificationUnverified,
		},
	}
}

func defaultConfiguration(manifest Manifest) Configuration {
	return Configuration{
		ID:        manifest.ID,
		Ownership: manifest.Ownership[0],
		Desired:   DesiredDisabled,
	}
}

func ValidateConfiguration(registry *Registry, configuration Configuration) error {
	if registry == nil {
		return fmt.Errorf("capability registry is required")
	}
	manifest, ok := registry.Get(configuration.ID)
	if !ok {
		return fmt.Errorf("unknown capability %q", configuration.ID)
	}
	if configuration.Desired != DesiredDisabled && configuration.Desired != DesiredEnabled {
		return fmt.Errorf("unknown desired state %q", configuration.Desired)
	}
	if !containsOwnership(manifest.Ownership, configuration.Ownership) {
		return fmt.Errorf("ownership %q is not supported by capability %q", configuration.Ownership, configuration.ID)
	}
	encoded, err := json.Marshal(configuration.Values)
	if err != nil {
		return fmt.Errorf("encode configured values: %w", err)
	}
	if len(encoded) > MaxInstanceConfigBytes {
		return fmt.Errorf("configured values exceed %d bytes", MaxInstanceConfigBytes)
	}

	fields := make(map[string]ConfigField, len(manifest.Config.Fields))
	for _, field := range manifest.Config.Fields {
		fields[field.Name] = field
	}
	keys := make([]string, 0, len(configuration.Values))
	for name := range configuration.Values {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		field, ok := fields[name]
		if !ok {
			return fmt.Errorf("unknown config field %q", name)
		}
		if err := validateConfiguredValue(field, configuration.Values[name]); err != nil {
			return fmt.Errorf("config field %q: %w", name, err)
		}
	}
	if configuration.Desired == DesiredEnabled {
		for _, field := range manifest.Config.Fields {
			if field.Required {
				if _, ok := configuration.Values[field.Name]; !ok {
					return fmt.Errorf("required config field %q is missing while enabled", field.Name)
				}
			}
		}
	}
	return nil
}

func validateConfiguredValue(field ConfigField, raw json.RawMessage) error {
	if len(raw) == 0 {
		return fmt.Errorf("value is empty")
	}
	switch field.Type {
	case ConfigString:
		var value string
		if err := decodeConfiguredScalar(raw, &value); err != nil {
			return err
		}
		if len(value) > 2048 || hasUnsafeText(value) {
			return fmt.Errorf("string value is invalid")
		}
		if field.Required && value == "" {
			return fmt.Errorf("required string value is empty")
		}
	case ConfigBoolean:
		var value bool
		if err := decodeConfiguredScalar(raw, &value); err != nil {
			return err
		}
	case ConfigInteger:
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err != nil {
			return fmt.Errorf("expected integer: %w", err)
		}
		if err := ensureJSONEOF(decoder); err != nil {
			return err
		}
		number, ok := decoded.(json.Number)
		if !ok {
			return fmt.Errorf("expected integer")
		}
		if _, err := number.Int64(); err != nil {
			return fmt.Errorf("expected integer")
		}
	case ConfigEnum:
		var value string
		if err := decodeConfiguredScalar(raw, &value); err != nil {
			return err
		}
		allowed := false
		for _, candidate := range field.Enum {
			if value == candidate {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("value %q is not in the allowed enum", value)
		}
	case ConfigSecretRef:
		var value string
		if err := decodeConfiguredScalar(raw, &value); err != nil {
			return err
		}
		if _, err := secretstore.ParseReference(value); err != nil {
			return fmt.Errorf("invalid secret reference: %w", err)
		}
	default:
		return fmt.Errorf("unsupported config field type %q", field.Type)
	}
	return nil
}

func decodeConfiguredScalar(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid value: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	return nil
}

func cloneConfiguration(configuration Configuration) Configuration {
	out := configuration
	if configuration.Values != nil {
		out.Values = make(map[string]json.RawMessage, len(configuration.Values))
		for name, value := range configuration.Values {
			out.Values[name] = append(json.RawMessage(nil), value...)
		}
	}
	return out
}
