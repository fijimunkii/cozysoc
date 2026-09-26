// Package incident builds conservative, deterministic incident proposals from
// normalized findings. It does not send notifications or infer device identity.
package incident

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const (
	MaxFindings = 128
	Window      = 15 * time.Minute
)

var ErrInput = errors.New("invalid incident correlation input")

var token = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)
var source = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// Signal carries the identity decision made by the finding producer. Only a
// user-confirmed device identity can connect different findings; an inferred
// device or IP address is never promoted into a correlation key.
type Signal struct {
	Finding           domain.Finding
	DeviceID          string
	IdentityAuthority domain.LinkAuthority
	SourceFamily      string
}

type Incident struct {
	ID                     string    `json:"id"`
	ScopeID                string    `json:"scope_id"`
	DeviceID               string    `json:"device_id,omitempty"`
	WindowStart            time.Time `json:"window_start"`
	WindowEnd              time.Time `json:"window_end"`
	Severity               string    `json:"severity"`
	Confidence             *float64  `json:"confidence,omitempty"`
	FindingIDs             []string  `json:"finding_ids"`
	EvidenceObservationIDs []string  `json:"evidence_observation_ids"`
	SourceFamilies         []string  `json:"source_families"`
	CorrelationReason      string    `json:"correlation_reason"`
}

type group struct {
	incident Incident
	unknown  bool
}

// Correlate is order-independent and idempotent. Fixed UTC windows keep the
// incident ID stable when a finding arrives late. The boundary is deliberately
// conservative: nearby findings across windows remain separate.
func Correlate(inputs []Signal) ([]Incident, error) {
	if len(inputs) > MaxFindings {
		return nil, ErrInput
	}
	seen := make(map[string]Signal, len(inputs))
	groups := make(map[string]*group, len(inputs))
	for _, input := range inputs {
		if err := validateSignal(input); err != nil {
			return nil, err
		}
		finding := input.Finding
		if previous, ok := seen[finding.ID]; ok {
			if !sameSignal(previous, input) {
				return nil, fmt.Errorf("%w: conflicting finding ID", ErrInput)
			}
			continue
		}
		seen[finding.ID] = input
		start := finding.ObservedAt.UTC().Truncate(Window)
		device := ""
		key := "finding\x00" + finding.ID
		if input.IdentityAuthority == domain.LinkUser {
			device = input.DeviceID
			if correlatableCategory(finding.Category) {
				key = "device\x00" + finding.ScopeID + "\x00" + device + "\x00" + start.Format(time.RFC3339)
			}
		}
		current := groups[key]
		if current == nil {
			digest := sha256.Sum256([]byte(key))
			current = &group{incident: Incident{ID: "incident." + hex.EncodeToString(digest[:16]), ScopeID: finding.ScopeID,
				DeviceID: device, WindowStart: start, WindowEnd: start.Add(Window), Severity: finding.Severity,
				CorrelationReason: "single-finding"}}
			groups[key] = current
		}
		item := &current.incident
		item.FindingIDs = append(item.FindingIDs, finding.ID)
		item.EvidenceObservationIDs = append(item.EvidenceObservationIDs, finding.EvidenceObservationIDs...)
		item.SourceFamilies = append(item.SourceFamilies, input.SourceFamily)
		if severityRank(finding.Severity) > severityRank(item.Severity) {
			item.Severity = finding.Severity
		}
		if finding.Confidence == nil {
			current.unknown = true
		} else if item.Confidence == nil || *finding.Confidence > *item.Confidence {
			value := *finding.Confidence
			item.Confidence = &value
		}
	}
	output := make([]Incident, 0, len(groups))
	for _, current := range groups {
		item := current.incident
		slices.Sort(item.FindingIDs)
		slices.Sort(item.EvidenceObservationIDs)
		item.EvidenceObservationIDs = slices.Compact(item.EvidenceObservationIDs)
		slices.Sort(item.SourceFamilies)
		item.SourceFamilies = slices.Compact(item.SourceFamilies)
		if current.unknown {
			item.Confidence = nil
		}
		if len(item.FindingIDs) > 1 {
			item.CorrelationReason = "same-user-confirmed-device-and-utc-window"
		}
		output = append(output, item)
	}
	sort.Slice(output, func(i, j int) bool {
		if output[i].WindowStart.Equal(output[j].WindowStart) {
			return output[i].ID < output[j].ID
		}
		return output[i].WindowStart.Before(output[j].WindowStart)
	})
	return output, nil
}

func validateSignal(input Signal) error {
	if err := domain.ValidateFinding(input.Finding); err != nil || !source.MatchString(input.SourceFamily) || severityRank(input.Finding.Severity) == 0 {
		return ErrInput
	}
	if input.DeviceID == "" {
		if input.IdentityAuthority != "" {
			return ErrInput
		}
		return nil
	}
	if !token.MatchString(input.DeviceID) || (input.IdentityAuthority != domain.LinkUser && input.IdentityAuthority != domain.LinkInferred) {
		return ErrInput
	}
	return nil
}

func sameSignal(a, b Signal) bool {
	if a.DeviceID != b.DeviceID || a.IdentityAuthority != b.IdentityAuthority || a.SourceFamily != b.SourceFamily {
		return false
	}
	// Finding payload is deliberately not decoded here. The normalized value is
	// compared exactly so a replay cannot silently replace evidence.
	return reflect.DeepEqual(a.Finding, b.Finding)
}

func correlatableCategory(category string) bool {
	switch category {
	case "new-device", "suspicious-connection", "tripwire":
		return true
	default:
		return false
	}
}

func severityRank(value string) int {
	switch value {
	case "informational":
		return 1
	case "low":
		return 2
	case "medium":
		return 3
	case "high":
		return 4
	case "critical":
		return 5
	default:
		return 0
	}
}
