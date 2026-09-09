package capability

import (
	"fmt"
	"sort"
)

type Registry struct {
	byID map[string]Manifest
}

func NewRegistry(manifests ...Manifest) (*Registry, error) {
	registry := &Registry{byID: make(map[string]Manifest, len(manifests))}
	for _, manifest := range manifests {
		if err := Validate(manifest); err != nil {
			return nil, fmt.Errorf("capability %q: %w", manifest.ID, err)
		}
		if _, exists := registry.byID[manifest.ID]; exists {
			return nil, fmt.Errorf("duplicate capability id %q", manifest.ID)
		}
		registry.byID[manifest.ID] = cloneManifest(manifest)
	}
	return registry, nil
}

func (r *Registry) Get(id string) (Manifest, bool) {
	if r == nil {
		return Manifest{}, false
	}
	manifest, ok := r.byID[id]
	if !ok {
		return Manifest{}, false
	}
	return cloneManifest(manifest), true
}

func (r *Registry) List() []Manifest {
	if r == nil {
		return nil
	}
	ids := make([]string, 0, len(r.byID))
	for id := range r.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Manifest, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneManifest(r.byID[id]))
	}
	return out
}

type Environment struct {
	OS        string
	Arch      string
	OSVersion string
}

type TargetMatch struct {
	Matched bool
	Target  Target
	Reason  string
}

func MatchTarget(manifest Manifest, env Environment) TargetMatch {
	for _, target := range manifest.Targets {
		if target.OS != env.OS || target.Arch != env.Arch {
			continue
		}
		if target.MinVersion == "" {
			return TargetMatch{Matched: true, Target: target}
		}
		actual, err := parseVersion(env.OSVersion)
		if err != nil {
			return TargetMatch{Target: target, Reason: "OS version is missing or invalid"}
		}
		minimum, _ := parseVersion(target.MinVersion)
		if compareVersion(actual, minimum) < 0 {
			return TargetMatch{Target: target, Reason: fmt.Sprintf("requires %s %s or newer", target.OS, target.MinVersion)}
		}
		return TargetMatch{Matched: true, Target: target}
	}
	return TargetMatch{Reason: fmt.Sprintf("no target for %s/%s", env.OS, env.Arch)}
}
