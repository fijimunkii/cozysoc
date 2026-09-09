package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"unicode"
)

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)

func ValidateNetworkScope(scope NetworkScope) error {
	if err := validateID("network scope id", scope.ID); err != nil {
		return err
	}
	if err := validateToken("network scope kind", scope.Kind, 64); err != nil {
		return err
	}
	if scope.EnrolledAt.IsZero() {
		return fmt.Errorf("network scope enrolled_at is required")
	}
	if scope.RetiredAt != nil && scope.RetiredAt.Before(scope.EnrolledAt) {
		return fmt.Errorf("network scope retired_at precedes enrolled_at")
	}
	return validateJSON("network scope metadata", scope.Metadata)
}

func ValidateSensor(sensor Sensor) error {
	if err := validateID("sensor id", sensor.ID); err != nil {
		return err
	}
	if err := validateID("sensor scope id", sensor.ScopeID); err != nil {
		return err
	}
	if err := validateToken("sensor kind", sensor.Kind, 64); err != nil {
		return err
	}
	if err := validateToken("sensor ownership", sensor.Ownership, 64); err != nil {
		return err
	}
	if sensor.RegisteredAt.IsZero() {
		return fmt.Errorf("sensor registered_at is required")
	}
	return validateJSON("sensor metadata", sensor.Metadata)
}

func ValidateObservation(observation Observation) error {
	for label, value := range map[string]string{
		"observation id":        observation.ID,
		"observation scope id":  observation.ScopeID,
		"observation sensor id": observation.SensorID,
	} {
		if err := validateID(label, value); err != nil {
			return err
		}
	}
	if err := validateToken("observation kind", observation.Kind, 96); err != nil {
		return err
	}
	if err := validateOpaque("observation source_stream", observation.SourceStream, 128); err != nil {
		return err
	}
	if err := validateOpaque("observation source_key", observation.SourceKey, 512); err != nil {
		return err
	}
	if observation.SourceEventID != "" {
		if err := validateOpaque("observation source_event_id", observation.SourceEventID, 512); err != nil {
			return err
		}
	}
	if observation.IngestedAt.IsZero() {
		return fmt.Errorf("observation ingested_at is required")
	}
	if observation.SchemaVersion <= 0 {
		return fmt.Errorf("observation schema_version must be positive")
	}
	if err := validateConfidence(observation.Confidence); err != nil {
		return fmt.Errorf("observation confidence: %w", err)
	}
	if err := validateOpaque("observation attribution", observation.Attribution, 256); err != nil {
		return err
	}
	if err := validateJSON("observation payload", observation.Payload); err != nil {
		return err
	}
	return validateRetention(observation.Retention)
}

func ValidateDevice(device Device) error {
	if err := validateID("device id", device.ID); err != nil {
		return err
	}
	if device.UserLabel != "" {
		if err := validateOpaque("device user_label", device.UserLabel, 160); err != nil {
			return err
		}
	}
	if device.CreatedAt.IsZero() {
		return fmt.Errorf("device created_at is required")
	}
	if device.RetiredAt != nil && device.RetiredAt.Before(device.CreatedAt) {
		return fmt.Errorf("device retired_at precedes created_at")
	}
	return nil
}

func ValidateIdentityClaim(claim IdentityClaim) error {
	if err := validateID("identity claim id", claim.ID); err != nil {
		return err
	}
	if err := validateID("identity claim scope id", claim.ScopeID); err != nil {
		return err
	}
	if err := validateID("identity claim sensor id", claim.SourceSensorID); err != nil {
		return err
	}
	if claim.SourceObservationID != "" {
		if err := validateID("identity claim observation id", claim.SourceObservationID); err != nil {
			return err
		}
	}
	if claim.ObservedAt.IsZero() {
		return fmt.Errorf("identity claim observed_at is required")
	}
	if claim.ValidUntil != nil && claim.ValidUntil.Before(claim.ObservedAt) {
		return fmt.Errorf("identity claim valid_until precedes observed_at")
	}
	if _, err := NormalizeClaimValue(claim.Kind, claim.Value); err != nil {
		return err
	}
	if err := validateConfidence(claim.Confidence); err != nil {
		return fmt.Errorf("identity claim confidence: %w", err)
	}
	return validateRetention(claim.Retention)
}

func ValidateDeviceClaimLink(link DeviceClaimLink) error {
	for label, value := range map[string]string{
		"device claim link id":        link.ID,
		"device claim link device id": link.DeviceID,
		"device claim link claim id":  link.ClaimID,
	} {
		if err := validateID(label, value); err != nil {
			return err
		}
	}
	if link.EvidenceObservationID != "" {
		if err := validateID("device claim link evidence observation id", link.EvidenceObservationID); err != nil {
			return err
		}
	}
	if link.ValidFrom.IsZero() {
		return fmt.Errorf("device claim link valid_from is required")
	}
	if link.ValidUntil != nil && link.ValidUntil.Before(link.ValidFrom) {
		return fmt.Errorf("device claim link valid_until precedes valid_from")
	}
	if err := validateConfidence(link.Confidence); err != nil {
		return fmt.Errorf("device claim link confidence: %w", err)
	}
	switch link.Authority {
	case LinkInferred, LinkUser:
	default:
		return fmt.Errorf("unknown device claim link authority %q", link.Authority)
	}
	return validateOpaque("device claim link reason", link.Reason, 512)
}

func ValidateCoverageSample(sample CoverageSample) error {
	for label, value := range map[string]string{
		"coverage sample id":            sample.ID,
		"coverage sample scope id":      sample.ScopeID,
		"coverage sample sensor id":     sample.SensorID,
		"coverage sample capability id": sample.CapabilityID,
	} {
		if err := validateID(label, value); err != nil {
			return err
		}
	}
	if err := validateToken("coverage status", sample.Status, 64); err != nil {
		return err
	}
	if sample.StartedAt.IsZero() || sample.EndedAt.IsZero() {
		return fmt.Errorf("coverage interval is required")
	}
	if sample.EndedAt.Before(sample.StartedAt) {
		return fmt.Errorf("coverage ended_at precedes started_at")
	}
	if sample.SchemaVersion <= 0 {
		return fmt.Errorf("coverage schema_version must be positive")
	}
	if err := validateJSON("coverage evidence", sample.Evidence); err != nil {
		return err
	}
	return validateRetention(sample.Retention)
}

func ValidateFinding(finding Finding) error {
	if err := validateID("finding id", finding.ID); err != nil {
		return err
	}
	if err := validateID("finding scope id", finding.ScopeID); err != nil {
		return err
	}
	if err := validateToken("finding detector id", finding.DetectorID, 128); err != nil {
		return err
	}
	if err := validateOpaque("finding detector version", finding.DetectorVersion, 128); err != nil {
		return err
	}
	if err := validateToken("finding category", finding.Category, 128); err != nil {
		return err
	}
	if err := validateToken("finding severity", finding.Severity, 32); err != nil {
		return err
	}
	if err := validateConfidence(finding.Confidence); err != nil {
		return fmt.Errorf("finding confidence: %w", err)
	}
	if finding.ObservedAt.IsZero() || finding.CreatedAt.IsZero() {
		return fmt.Errorf("finding observed_at and created_at are required")
	}
	if finding.SchemaVersion <= 0 {
		return fmt.Errorf("finding schema_version must be positive")
	}
	if err := validateJSON("finding payload", finding.Payload); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, id := range finding.EvidenceObservationIDs {
		if err := validateID("finding evidence observation id", id); err != nil {
			return err
		}
		if seen[id] {
			return fmt.Errorf("duplicate finding evidence observation id %q", id)
		}
		seen[id] = true
	}
	return validateRetention(finding.Retention)
}

func ValidateAuditEvent(event AuditEvent) error {
	if err := validateID("audit event id", event.ID); err != nil {
		return err
	}
	if err := validateToken("audit event kind", event.Kind, 128); err != nil {
		return err
	}
	if err := validateOpaque("audit event actor", event.Actor, 128); err != nil {
		return err
	}
	if event.OccurredAt.IsZero() {
		return fmt.Errorf("audit event occurred_at is required")
	}
	if event.SchemaVersion <= 0 {
		return fmt.Errorf("audit event schema_version must be positive")
	}
	if err := validateJSON("audit event payload", event.Payload); err != nil {
		return err
	}
	return validateRetention(event.Retention)
}

func ValidateCheckpoint(checkpoint IngestionCheckpoint) error {
	if err := validateID("checkpoint sensor id", checkpoint.SensorID); err != nil {
		return err
	}
	if err := validateOpaque("checkpoint stream id", checkpoint.StreamID, 128); err != nil {
		return err
	}
	if err := validateOpaque("checkpoint cursor", checkpoint.Cursor, 4096); err != nil {
		return err
	}
	if checkpoint.UpdatedAt.IsZero() {
		return fmt.Errorf("checkpoint updated_at is required")
	}
	return nil
}

func NormalizeClaimValue(kind ClaimKind, value string) (string, error) {
	switch kind {
	case ClaimIPv4:
		address, err := netip.ParseAddr(value)
		if err != nil || !address.Is4() {
			return "", fmt.Errorf("invalid IPv4 identity claim %q", value)
		}
		return address.String(), nil
	case ClaimIPv6:
		address, err := netip.ParseAddr(value)
		if err != nil || !address.Is6() {
			return "", fmt.Errorf("invalid IPv6 identity claim %q", value)
		}
		return address.String(), nil
	case ClaimMAC:
		address, err := net.ParseMAC(value)
		if err != nil {
			return "", fmt.Errorf("invalid MAC identity claim %q", value)
		}
		return strings.ToLower(address.String()), nil
	case ClaimHostname, ClaimServiceName, ClaimDHCPClientID, ClaimEndpointID:
		if err := validateOpaque("identity claim value", value, 512); err != nil {
			return "", err
		}
		return value, nil
	default:
		return "", fmt.Errorf("unknown identity claim kind %q", kind)
	}
}

func validateRetention(retention RetentionClass) error {
	switch retention {
	case RetentionEphemeral, RetentionShort, RetentionStandard, RetentionAudit:
		return nil
	default:
		return fmt.Errorf("unknown retention class %q", retention)
	}
}

func validateID(label, value string) error {
	if !idPattern.MatchString(value) {
		return fmt.Errorf("invalid %s %q", label, value)
	}
	return nil
}

func validateToken(label, value string, max int) error {
	if value == "" || len(value) > max || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is empty, oversized, or untrimmed", label)
	}
	for _, r := range value {
		if !(unicode.IsLower(r) || unicode.IsDigit(r) || r == '-' || r == '.' || r == '_' || r == ':') {
			return fmt.Errorf("%s contains unsupported characters", label)
		}
	}
	return nil
}

func validateOpaque(label, value string, max int) error {
	if value == "" || len(value) > max || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is empty, oversized, or untrimmed", label)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", label)
		}
	}
	return nil
}

func validateConfidence(value *float64) error {
	if value == nil {
		return nil
	}
	if *value < 0 || *value > 1 {
		return fmt.Errorf("must be between 0 and 1")
	}
	return nil
}

func validateJSON(label string, value json.RawMessage) error {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 {
		return fmt.Errorf("%s is required", label)
	}
	if len(trimmed) > MaxJSONBytes {
		return fmt.Errorf("%s exceeds %d bytes", label, MaxJSONBytes)
	}
	if !json.Valid(trimmed) {
		return fmt.Errorf("%s is not valid JSON", label)
	}
	return nil
}
