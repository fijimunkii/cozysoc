package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"unicode"
)

type storedDeviceWatchBinding struct {
	InterfaceName  string   `json:"interface_name"`
	InterfaceIndex int      `json:"interface_index"`
	Prefixes       []string `json:"prefixes"`
}

func validateStoredDeviceWatchBinding(raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var binding storedDeviceWatchBinding
	if err := decoder.Decode(&binding); err != nil {
		return fmt.Errorf("decode Device Watch network binding: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("Device Watch network binding contains trailing JSON")
	}
	if err := validateStoredInterfaceName(binding.InterfaceName); err != nil {
		return err
	}
	if binding.InterfaceIndex <= 0 {
		return fmt.Errorf("Device Watch network binding interface index must be positive")
	}
	if len(binding.Prefixes) == 0 || len(binding.Prefixes) > 32 {
		return fmt.Errorf("Device Watch network binding must contain between 1 and 32 prefixes")
	}
	seen := make(map[string]struct{}, len(binding.Prefixes))
	for _, rawPrefix := range binding.Prefixes {
		prefix, err := netip.ParsePrefix(rawPrefix)
		if err != nil || prefix != prefix.Masked() || !usableStoredPrefix(prefix) {
			return fmt.Errorf("invalid Device Watch enrolled prefix %q", rawPrefix)
		}
		canonical := prefix.String()
		if _, ok := seen[canonical]; ok {
			return fmt.Errorf("duplicate Device Watch enrolled prefix %q", canonical)
		}
		seen[canonical] = struct{}{}
	}
	return nil
}

func validateStoredInterfaceName(value string) error {
	if value == "" || len(value) > 64 || strings.TrimSpace(value) != value {
		return fmt.Errorf("invalid Device Watch interface name")
	}
	for index, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("invalid Device Watch interface name")
		}
		if index == 0 && !(unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return fmt.Errorf("invalid Device Watch interface name")
		}
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' || r == ':') {
			return fmt.Errorf("invalid Device Watch interface name")
		}
	}
	return nil
}

func usableStoredPrefix(prefix netip.Prefix) bool {
	if !prefix.IsValid() {
		return false
	}
	address := prefix.Addr()
	return !address.IsUnspecified() && !address.IsLoopback() && !address.IsMulticast()
}
