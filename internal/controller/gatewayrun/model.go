// Package gatewayrun controls one reviewed gateway run. It contains no packet
// sender, installed executor, IPC endpoint, background worker, or persisted
// approval. Its collaborators are trusted controller code, never browser input.
package gatewayrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"slices"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/gatewayicmp"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

var (
	ErrUnavailable = errors.New("gateway run control is unavailable")
	ErrBusy        = errors.New("gateway run control is busy")
	ErrCooldown    = errors.New("gateway run cooldown is active")
	ErrConsent     = errors.New("explicit one-shot gateway consent is required")
	ErrReview      = errors.New("gateway review is unknown, consumed, or expired")
	ErrPreflight   = errors.New("gateway run preflight is unavailable or changed")
	ErrAudit       = errors.New("gateway run audit could not be confirmed; run control is locked")
	ErrClock       = errors.New("gateway run clock is invalid; run control is locked")
	ErrExecution   = errors.New("gateway execution did not complete; approval is consumed")
)

const (
	Profile            = "gateway-icmp-v1"
	EventSchemaVersion = 2
	RunInterval        = time.Minute
	OperationTimeout   = 5 * time.Second
	AuditTimeout       = time.Second
)

// Selection is produced by a trusted preflight, not decoded from a review DTO.
// Route consistency alone is insufficient: an executor must independently
// revalidate and enforce its actual socket binding before EVERY packet send.
type Selection struct {
	Plan            networkquality.GatewayCheckPlan
	Source          netip.Addr
	RouteObservedAt time.Time
	RouteFreshUntil time.Time
}

type Preflight func(context.Context, netip.Addr) (Selection, error)

// Executor is an internal, narrowly scoped one-shot collaborator. There is no
// installed production implementation yet. Implementations must honor cancellation
// and fixed per-attempt/byte/receive budgets, without retries or detached work.
// nil means unavailable, not permission to use a fallback executable or sender.
type Executor interface {
	ExecuteGateway(context.Context, Selection) (gatewayicmp.Sample, error)
}
type Auditor interface {
	InsertGatewayRunAudit(context.Context, Event) error
}

type Ticket struct{ key [32]byte }

func (Ticket) String() string               { return "[gateway review ticket]" }
func (Ticket) GoString() string             { return "[gateway review ticket]" }
func (Ticket) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }

// A Ticket is process-local and deliberately has no wire decoder. The returned
// selection is a copy for review; Run accepts only the ticket and explicit consent.
type Review struct {
	Ticket    Ticket
	Selection Selection
	ExpiresAt time.Time
}

// Result describes controlled execution, never connectivity or a security finding.
// Completed means a complete, validated sample and its terminal audit committed,
// not that any reply arrived. Sample is absent before measurement or when evidence
// is invalid/indeterminate. No result is returned on unconfirmed terminal audit.
type Result struct {
	RunID   string
	Outcome string
	Sample  *gatewayicmp.Sample
}

type Event struct {
	SchemaVersion   int          `json:"schema_version"`
	Measurement     *Measurement `json:"measurement,omitempty"`
	RunID           string       `json:"run_id"`
	State           string       `json:"state"`
	Outcome         string       `json:"outcome,omitempty"`
	Reason          string       `json:"reason,omitempty"`
	At              time.Time    `json:"at"`
	Profile         string       `json:"profile"`
	SelectionDigest string       `json:"selection_digest"`
	ScopeID         string       `json:"scope_id"`
	InterfaceName   string       `json:"interface_name"`
	InterfaceIndex  int          `json:"interface_index"`
	Target          string       `json:"target"`
	Source          string       `json:"source"`
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)
var interfacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)
var hexID = regexp.MustCompile(`^[0-9a-f]{32}$`)
var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validTime(t time.Time) bool {
	// Audit storage uses signed nanoseconds. Reject values that cannot round-trip.
	return !t.IsZero() && time.Unix(0, t.UnixNano()).Equal(t)
}

// ValidateEvent bounds the persistent payload and excludes arbitrary diagnostics,
// credentials, review tickets, raw packets, and network-wide success claims.
func ValidateEvent(e Event) error {
	if (e.SchemaVersion != 1 && e.SchemaVersion != EventSchemaVersion) || !hexID.MatchString(e.RunID) || !hexDigest.MatchString(e.SelectionDigest) ||
		e.Profile != Profile || !validTime(e.At) || !idPattern.MatchString(e.ScopeID) ||
		!interfacePattern.MatchString(e.InterfaceName) || e.InterfaceIndex < 1 || e.InterfaceIndex > 2147483647 ||
		networkquality.ValidateGatewayPreviewTarget(e.Target) != nil || networkquality.ValidateGatewayPreviewTarget(e.Source) != nil || e.Source == e.Target {
		return ErrAudit
	}
	// Version 1 describes legacy execution-only audits, never measured success.
	if e.SchemaVersion == 1 && e.Measurement != nil {
		return ErrAudit
	}
	if e.Measurement != nil {
		if e.State != "finished" || (e.Outcome != "completed" && e.Outcome != "failed" && e.Outcome != "canceled") ||
			validateMeasurement(*e.Measurement, e.At) != nil || (e.Outcome == "failed" && e.Measurement.Complete) {
			return ErrAudit
		}
	}
	if e.SchemaVersion == EventSchemaVersion && e.State == "finished" && e.Outcome == "completed" &&
		(e.Measurement == nil || !e.Measurement.Complete) {
		return ErrAudit
	}
	switch e.State {
	case "authorized", "admitted":
		if e.Outcome == "" && e.Reason == "" {
			return nil
		}
	case "finished":
		switch e.Outcome {
		case "completed":
			if e.Reason == "" {
				return nil
			}
		case "blocked":
			if e.Reason == "preflight-unavailable" || e.Reason == "selection-changed" || e.Reason == "review-expired" || e.Reason == "clock-invalid" {
				return nil
			}
		case "canceled":
			if e.Reason == "canceled" {
				return nil
			}
		case "failed":
			if e.Reason == "execution-error" || (e.SchemaVersion == EventSchemaVersion && e.Reason == "measurement-invalid" && e.Measurement == nil) {
				return nil
			}
		case "indeterminate":
			if e.Reason == "execution-panic" {
				return nil
			}
		}
	}
	return ErrAudit
}

func normalize(s Selection, target netip.Addr, now time.Time) (Selection, error) {
	if !validTime(now) || !validTime(s.Plan.CreatedAt) || !validTime(s.RouteObservedAt) ||
		s.Plan.Target != target || s.Plan.CreatedAt.After(now) || s.RouteObservedAt.Before(s.Plan.CreatedAt) || s.RouteObservedAt.After(now) ||
		!s.RouteFreshUntil.Equal(s.RouteObservedAt.Add(networkquality.GatewayReviewLifetime)) || !s.RouteFreshUntil.After(now) || s.Source == target {
		return Selection{}, ErrPreflight
	}
	plan, err := networkquality.PreviewGatewayCheck(s.Plan.Binding, target.String(), s.Plan.CreatedAt)
	if err != nil || s.Plan.Budget != plan.Budget || !s.Plan.ReviewExpiresAt.Equal(plan.ReviewExpiresAt) || !plan.ReviewExpiresAt.After(now) {
		return Selection{}, ErrPreflight
	}
	if _, err := networkquality.PreviewGatewayCheck(plan.Binding, s.Source.String(), now); err != nil {
		return Selection{}, ErrPreflight
	}
	s.Plan = plan
	return s, nil
}
func copySelection(s Selection) Selection {
	s.Plan.Binding.Prefixes = slices.Clone(s.Plan.Binding.Prefixes)
	return s
}
func sameSelection(a, b Selection) bool {
	x, y := a.Plan.Binding, b.Plan.Binding
	return x.ScopeID == y.ScopeID && x.InterfaceName == y.InterfaceName && x.InterfaceIndex == y.InterfaceIndex &&
		slices.Equal(x.Prefixes, y.Prefixes) && a.Plan.Target == b.Plan.Target && a.Source == b.Source && a.Plan.Budget == b.Plan.Budget
}
func selectionDigest(s Selection) string {
	// Timestamps are excluded: fresh revalidation must preserve scope/source/target
	// and all budgets, while acquiring new evidence timestamps.
	raw, _ := json.Marshal(struct {
		Binding        networkquality.GatewayPlanBinding
		Target, Source string
		Budget         networkquality.GatewayProbeBudget
		Profile        string
	}{
		s.Plan.Binding, s.Plan.Target.String(), s.Source.String(), s.Plan.Budget, Profile,
	})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
