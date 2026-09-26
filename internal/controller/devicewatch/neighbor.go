package devicewatch

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	MaxNeighborEntries = 4096
	MaxSnapshotBytes   = 1 << 20
	maxSnapshotLine    = 4096
)

var (
	ErrSnapshotUnavailable = errors.New("device watch neighbor snapshot is unavailable")
	ErrSnapshotPermission  = errors.New("device watch neighbor snapshot requires permission")
	ErrSnapshotTooLarge    = errors.New("device watch neighbor snapshot exceeds safety limits")
)

type NeighborMethod string

const (
	MethodARPCache NeighborMethod = "arp-cache"
	MethodNDPCache NeighborMethod = "ndp-cache"
)

type Neighbor struct {
	Address       netip.Addr
	HardwareAddr  net.HardwareAddr
	InterfaceName string
	Method        NeighborMethod
	State         string
}

type SourceStatus struct {
	Method             NeighborMethod
	Available          bool
	PermissionRequired bool
}

// snapshotFailure preserves per-source health without carrying command output
// into retained coverage evidence.
type snapshotFailure struct {
	Sources []SourceStatus
	Cause   error
}

func (f *snapshotFailure) Error() string { return f.Cause.Error() }
func (f *snapshotFailure) Unwrap() error { return f.Cause }

type Snapshot struct {
	CapturedAt    time.Time
	InterfaceName string
	Neighbors     []Neighbor
	Sources       []SourceStatus
}

type Snapshotter interface {
	Snapshot(context.Context, string) (Snapshot, error)
}

func parseARP(data []byte, interfaceName string) ([]Neighbor, error) {
	if len(data) > MaxSnapshotBytes {
		return nil, ErrSnapshotTooLarge
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1024), maxSnapshotLine)
	neighbors := make([]Neighbor, 0, 64)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		left := strings.IndexByte(line, '(')
		right := strings.IndexByte(line, ')')
		at := strings.Index(line, " at ")
		on := strings.Index(line, " on ")
		if left < 0 || right <= left || at <= right || on <= at {
			continue
		}
		addressText := line[left+1 : right]
		hardwareText := strings.Fields(line[at+4 : on])
		interfaceFields := strings.Fields(line[on+4:])
		if len(hardwareText) == 0 || len(interfaceFields) == 0 || interfaceFields[0] != interfaceName {
			continue
		}
		if strings.EqualFold(hardwareText[0], "(incomplete)") || strings.EqualFold(hardwareText[0], "incomplete") {
			continue
		}
		address, err := netip.ParseAddr(addressText)
		if err != nil || !address.Is4() {
			continue
		}
		hardware, ok := parseMacOSLinkLayerAddress(hardwareText[0])
		if !ok {
			continue
		}
		neighbors = append(neighbors, Neighbor{
			Address:       address,
			HardwareAddr:  append(net.HardwareAddr(nil), hardware...),
			InterfaceName: interfaceName,
			Method:        MethodARPCache,
		})
		if len(neighbors) > MaxNeighborEntries {
			return nil, ErrSnapshotTooLarge
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse ARP snapshot: %w", err)
	}
	return deduplicateNeighbors(neighbors), nil
}

func parseNDP(data []byte, interfaceName string) ([]Neighbor, error) {
	if len(data) > MaxSnapshotBytes {
		return nil, ErrSnapshotTooLarge
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1024), maxSnapshotLine)
	neighbors := make([]Neighbor, 0, 64)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(strings.ToLower(line), "neighbor ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[2] != interfaceName {
			continue
		}
		address, err := netip.ParseAddr(fields[0])
		if err != nil || !address.Is6() {
			continue
		}
		hardware, ok := parseMacOSLinkLayerAddress(fields[1])
		if !ok {
			continue
		}
		state := ""
		if len(fields) >= 5 && len(fields[4]) <= 16 {
			state = fields[4]
		}
		neighbors = append(neighbors, Neighbor{
			Address:       address,
			HardwareAddr:  append(net.HardwareAddr(nil), hardware...),
			InterfaceName: interfaceName,
			Method:        MethodNDPCache,
			State:         state,
		})
		if len(neighbors) > MaxNeighborEntries {
			return nil, ErrSnapshotTooLarge
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse NDP snapshot: %w", err)
	}
	return deduplicateNeighbors(neighbors), nil
}

// macOS arp/ndp may omit the leading zero from an octet (for example, 0:2b).
// Keep the accepted format to exactly six colon-separated hex octets.
func parseMacOSLinkLayerAddress(value string) (net.HardwareAddr, bool) {
	octets := strings.Split(value, ":")
	if len(octets) != 6 {
		return nil, false
	}
	address := make(net.HardwareAddr, 6)
	for i, octet := range octets {
		if len(octet) < 1 || len(octet) > 2 {
			return nil, false
		}
		parsed, err := strconv.ParseUint(octet, 16, 8)
		if err != nil {
			return nil, false
		}
		address[i] = byte(parsed)
	}
	return address, true
}

func deduplicateNeighbors(neighbors []Neighbor) []Neighbor {
	sort.Slice(neighbors, func(i, j int) bool {
		left := neighborKey(neighbors[i])
		right := neighborKey(neighbors[j])
		return left < right
	})
	out := neighbors[:0]
	var previous string
	for _, neighbor := range neighbors {
		key := neighborKey(neighbor)
		if len(out) > 0 && key == previous {
			continue
		}
		out = append(out, neighbor)
		previous = key
	}
	return out
}

func neighborKey(neighbor Neighbor) string {
	return string(neighbor.Method) + "\x00" + neighbor.InterfaceName + "\x00" + neighbor.Address.String() + "\x00" + strings.ToLower(neighbor.HardwareAddr.String())
}
