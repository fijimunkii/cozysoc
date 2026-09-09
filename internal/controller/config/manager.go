package config

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"sync"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
)

// Manager serializes durable controller configuration updates inside the single
// controller process. It keeps config.json as the source of truth for durable
// capability intent.
type Manager struct {
	mu       sync.RWMutex
	stateDir string
	registry *capability.Registry
	current  Config
}

func NewManager(stateDir string, registry *capability.Registry, initial Config) (*Manager, error) {
	if registry == nil {
		return nil, fmt.Errorf("config manager requires capability registry")
	}
	if err := validate(initial); err != nil {
		return nil, err
	}
	for _, configured := range initial.Capabilities {
		if err := capability.ValidateConfiguration(registry, configured); err != nil {
			return nil, fmt.Errorf("capability configuration %q: %w", configured.ID, err)
		}
	}
	return &Manager{
		stateDir: stateDir,
		registry: registry,
		current:  cloneConfig(initial),
	}, nil
}

func (m *Manager) Snapshot() Config {
	if m == nil {
		return Config{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneConfig(m.current)
}

func (m *Manager) Capability(id string) (capability.Configuration, bool) {
	if m == nil {
		return capability.Configuration{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, configured := range m.current.Capabilities {
		if configured.ID == id {
			return cloneCapabilityConfiguration(configured), true
		}
	}
	return capability.Configuration{}, false
}

// ReplaceCapability atomically replaces one configured capability. A nil next
// removes the explicit configuration and restores manifest defaults on restart.
func (m *Manager) ReplaceCapability(id string, next *capability.Configuration) (*capability.Configuration, bool, error) {
	if m == nil || m.registry == nil {
		return nil, false, fmt.Errorf("config manager is unavailable")
	}
	if next != nil {
		if next.ID != id {
			return nil, false, fmt.Errorf("capability configuration id mismatch")
		}
		if err := capability.ValidateConfiguration(m.registry, *next); err != nil {
			return nil, false, err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	candidate := cloneConfig(m.current)
	index := -1
	var previous *capability.Configuration
	for i := range candidate.Capabilities {
		if candidate.Capabilities[i].ID == id {
			index = i
			copyValue := cloneCapabilityConfiguration(candidate.Capabilities[i])
			previous = &copyValue
			break
		}
	}

	if next == nil {
		if index < 0 {
			return nil, false, nil
		}
		candidate.Capabilities = append(candidate.Capabilities[:index], candidate.Capabilities[index+1:]...)
	} else {
		copyValue := cloneCapabilityConfiguration(*next)
		if previous != nil && reflect.DeepEqual(*previous, copyValue) {
			return previous, false, nil
		}
		if index >= 0 {
			candidate.Capabilities[index] = copyValue
		} else {
			candidate.Capabilities = append(candidate.Capabilities, copyValue)
		}
	}

	sort.Slice(candidate.Capabilities, func(i, j int) bool {
		return candidate.Capabilities[i].ID < candidate.Capabilities[j].ID
	})
	if err := validate(candidate); err != nil {
		return previous, false, err
	}
	if err := writeAtomic(filepath.Join(m.stateDir, Filename), candidate); err != nil {
		return previous, false, err
	}
	m.current = candidate
	return previous, true, nil
}

func cloneConfig(input Config) Config {
	out := input
	if input.Capabilities != nil {
		out.Capabilities = make([]capability.Configuration, len(input.Capabilities))
		for i, configured := range input.Capabilities {
			out.Capabilities[i] = cloneCapabilityConfiguration(configured)
		}
	}
	return out
}

func cloneCapabilityConfiguration(input capability.Configuration) capability.Configuration {
	out := input
	if input.Values != nil {
		out.Values = make(map[string]json.RawMessage, len(input.Values))
		for name, value := range input.Values {
			out.Values[name] = append(json.RawMessage(nil), value...)
		}
	}
	return out
}