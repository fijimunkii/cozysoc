package devicewatch

import (
	"context"
	"fmt"
	"net"
	"sort"
)

const MaxScopeCandidates = 64

func ValidateEnrollmentInterfaceName(value string) error {
	return validateInterfaceName(value)
}

// ListScopeCandidates returns local interfaces that can be explicitly enrolled
// for passive Device Watch. It only inspects local interface metadata; it does
// not send packets, resolve names, or otherwise probe the network.
func ListScopeCandidates(ctx context.Context, inspector InterfaceInspector) ([]ScopeBinding, bool, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, false, fmt.Errorf("list local interfaces: %w", err)
	}
	return scopeCandidatesFromInterfaces(ctx, inspector, interfaces)
}

func scopeCandidatesFromInterfaces(ctx context.Context, inspector InterfaceInspector, interfaces []net.Interface) ([]ScopeBinding, bool, error) {
	if inspector == nil {
		return nil, false, fmt.Errorf("interface inspector is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	sort.Slice(interfaces, func(i, j int) bool {
		if interfaces[i].Index == interfaces[j].Index {
			return interfaces[i].Name < interfaces[j].Name
		}
		return interfaces[i].Index < interfaces[j].Index
	})

	candidates := make([]ScopeBinding, 0, min(len(interfaces), MaxScopeCandidates))
	truncated := false
	for _, iface := range interfaces {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		binding, err := CaptureScopeBinding(ctx, inspector, iface.Name)
		if err != nil {
			continue
		}
		if len(candidates) == MaxScopeCandidates {
			truncated = true
			break
		}
		candidates = append(candidates, binding)
	}
	return candidates, truncated, nil
}
