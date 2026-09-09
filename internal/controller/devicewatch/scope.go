package devicewatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"unicode"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

var (
	ErrScopeMismatch        = errors.New("device watch network scope no longer matches the enrolled interface")
	ErrUnsupportedInterface = errors.New("device watch does not support this interface for passive discovery")
)

type ScopeBinding struct {
	InterfaceName  string   `json:"interface_name"`
	InterfaceIndex int      `json:"interface_index"`
	Prefixes       []string `json:"prefixes"`
}

type scopeMetadata struct {
	DeviceWatch *ScopeBinding `json:"device_watch,omitempty"`
}

type InterfaceState struct {
	Name     string
	Index    int
	Flags    net.Flags
	Prefixes []netip.Prefix
}

type InterfaceInspector interface {
	Inspect(context.Context, string) (InterfaceState, error)
}

type systemInterfaceInspector struct{}

func NewSystemInterfaceInspector() InterfaceInspector {
	return systemInterfaceInspector{}
}

func CaptureScopeBinding(ctx context.Context, inspector InterfaceInspector, interfaceName string) (ScopeBinding, error) {
	if inspector == nil {
		return ScopeBinding{}, fmt.Errorf("interface inspector is required")
	}
	if err := validateInterfaceName(interfaceName); err != nil {
		return ScopeBinding{}, err
	}
	state, err := inspector.Inspect(ctx, interfaceName)
	if err != nil {
		return ScopeBinding{}, err
	}
	if err := validateInterfaceState(state); err != nil {
		return ScopeBinding{}, err
	}
	prefixes := make([]string, 0, len(state.Prefixes))
	for _, prefix := range state.Prefixes {
		if !usablePrefix(prefix) {
			continue
		}
		prefixes = append(prefixes, prefix.Masked().String())
	}
	sort.Strings(prefixes)
	prefixes = compactStrings(prefixes)
	if len(prefixes) > 32 {
		return ScopeBinding{}, fmt.Errorf("%w: interface exposes too many IP prefixes", ErrUnsupportedInterface)
	}
	if len(prefixes) == 0 {
		return ScopeBinding{}, fmt.Errorf("%w: interface has no usable IP prefixes", ErrUnsupportedInterface)
	}
	return ScopeBinding{
		InterfaceName:  state.Name,
		InterfaceIndex: state.Index,
		Prefixes:       prefixes,
	}, nil
}

func EncodeScopeMetadata(binding ScopeBinding) (json.RawMessage, error) {
	if err := ValidateScopeBinding(binding); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(scopeMetadata{DeviceWatch: &binding})
	if err != nil {
		return nil, fmt.Errorf("encode device watch scope binding: %w", err)
	}
	return encoded, nil
}

func ParseScopeBinding(scope domain.NetworkScope) (ScopeBinding, error) {
	var metadata scopeMetadata
	if err := json.Unmarshal(scope.Metadata, &metadata); err != nil {
		return ScopeBinding{}, fmt.Errorf("decode device watch scope binding: %w", err)
	}
	if metadata.DeviceWatch == nil {
		return ScopeBinding{}, fmt.Errorf("network scope %q has no device watch binding", scope.ID)
	}
	binding := *metadata.DeviceWatch
	if err := ValidateScopeBinding(binding); err != nil {
		return ScopeBinding{}, err
	}
	return binding, nil
}

func ValidateScopeBinding(binding ScopeBinding) error {
	if err := validateInterfaceName(binding.InterfaceName); err != nil {
		return err
	}
	if binding.InterfaceIndex <= 0 {
		return fmt.Errorf("device watch interface index must be positive")
	}
	if len(binding.Prefixes) == 0 || len(binding.Prefixes) > 32 {
		return fmt.Errorf("device watch scope must contain between 1 and 32 prefixes")
	}
	seen := make(map[string]struct{}, len(binding.Prefixes))
	for _, raw := range binding.Prefixes {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || !usablePrefix(prefix) || prefix != prefix.Masked() {
			return fmt.Errorf("invalid enrolled prefix %q", raw)
		}
		canonical := prefix.String()
		if _, ok := seen[canonical]; ok {
			return fmt.Errorf("duplicate enrolled prefix %q", canonical)
		}
		seen[canonical] = struct{}{}
	}
	return nil
}

func ValidateCurrentScope(ctx context.Context, inspector InterfaceInspector, binding ScopeBinding) (InterfaceState, error) {
	if inspector == nil {
		return InterfaceState{}, fmt.Errorf("interface inspector is required")
	}
	if err := ValidateScopeBinding(binding); err != nil {
		return InterfaceState{}, err
	}
	state, err := inspector.Inspect(ctx, binding.InterfaceName)
	if err != nil {
		return InterfaceState{}, fmt.Errorf("%w: inspect enrolled interface", ErrScopeMismatch)
	}
	if err := validateInterfaceState(state); err != nil {
		return InterfaceState{}, err
	}
	if state.Name != binding.InterfaceName || state.Index != binding.InterfaceIndex {
		return InterfaceState{}, fmt.Errorf("%w: interface identity changed", ErrScopeMismatch)
	}

	enrolled := make(map[string]struct{}, len(binding.Prefixes))
	for _, raw := range binding.Prefixes {
		prefix, _ := netip.ParsePrefix(raw)
		enrolled[prefix.Masked().String()] = struct{}{}
	}
	for _, prefix := range state.Prefixes {
		if !usablePrefix(prefix) {
			continue
		}
		if _, ok := enrolled[prefix.Masked().String()]; ok {
			return state, nil
		}
	}
	return InterfaceState{}, fmt.Errorf("%w: enrolled IP prefixes are no longer present", ErrScopeMismatch)
}

func AddressInScope(binding ScopeBinding, address netip.Addr) bool {
	if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() || address.IsLoopback() {
		return false
	}
	candidate := address
	if candidate.Is6() {
		candidate = candidate.WithZone("")
	}
	for _, raw := range binding.Prefixes {
		prefix, err := netip.ParsePrefix(raw)
		if err == nil && prefix.Contains(candidate) {
			return true
		}
	}
	return false
}

func (systemInterfaceInspector) Inspect(ctx context.Context, interfaceName string) (InterfaceState, error) {
	if err := ctx.Err(); err != nil {
		return InterfaceState{}, err
	}
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return InterfaceState{}, fmt.Errorf("inspect interface %q: %w", interfaceName, err)
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return InterfaceState{}, fmt.Errorf("read interface %q addresses: %w", interfaceName, err)
	}
	prefixes := make([]netip.Prefix, 0, len(addresses))
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.String())
		if err != nil {
			continue
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return InterfaceState{Name: iface.Name, Index: iface.Index, Flags: iface.Flags, Prefixes: prefixes}, nil
}

func validateInterfaceState(state InterfaceState) error {
	if err := validateInterfaceName(state.Name); err != nil {
		return err
	}
	if state.Index <= 0 {
		return fmt.Errorf("%w: invalid interface index", ErrUnsupportedInterface)
	}
	if state.Flags&net.FlagUp == 0 {
		return fmt.Errorf("%w: interface is down", ErrScopeMismatch)
	}
	if state.Flags&net.FlagLoopback != 0 || state.Flags&net.FlagPointToPoint != 0 {
		return fmt.Errorf("%w: loopback and point-to-point interfaces are excluded", ErrUnsupportedInterface)
	}
	return nil
}

func validateInterfaceName(value string) error {
	if value == "" || len(value) > 64 || strings.TrimSpace(value) != value {
		return fmt.Errorf("invalid interface name")
	}
	for index, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("invalid interface name")
		}
		if index == 0 && !(unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return fmt.Errorf("invalid interface name")
		}
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' || r == ':') {
			return fmt.Errorf("invalid interface name")
		}
	}
	return nil
}

func usablePrefix(prefix netip.Prefix) bool {
	if !prefix.IsValid() {
		return false
	}
	address := prefix.Addr()
	return !address.IsUnspecified() && !address.IsLoopback() && !address.IsMulticast()
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
