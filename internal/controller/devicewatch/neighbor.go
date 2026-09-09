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
	Method    NeighborMethod
	Available bool
}

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
		hardware, err := net.ParseMAC(hardwareText[0])
		if err != nil || len(hardware) != 6 {
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
		hardware, err := net.ParseMAC(fields[1])
		if err != nil || len(hardware) != 6 {
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
