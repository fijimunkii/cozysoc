package networkquality

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Synthetic enrollment only. These tests contain no network or OS reads.
func gatewayPlanFixture() (GatewayPlanBinding, time.Time) {
	return GatewayPlanBinding{ScopeID: "scope.home", InterfaceName: "en0", InterfaceIndex: 7,
		Prefixes: []string{"fe80::/64", "192.168.50.0/24"}}, time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
}

func TestGatewayPreviewBindsTargetAndFixedBudgetWithoutAliasing(t *testing.T) {
	binding, now := gatewayPlanFixture()
	plan, err := PreviewGatewayCheck(binding, "192.168.50.1", now)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target.String() != "192.168.50.1" || plan.Binding.ScopeID != binding.ScopeID ||
		plan.Binding.InterfaceName != "en0" || plan.Binding.InterfaceIndex != 7 ||
		!plan.CreatedAt.Equal(now) || !plan.ReviewExpiresAt.Equal(now.Add(30*time.Second)) {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if plan.Budget != (GatewayProbeBudget{MaxAttempts: 3, MinInterval: time.Second, AttemptTimeout: time.Second,
		TotalTimeout: 5 * time.Second, PayloadBytes: 32, MaxICMPRequestBytes: 120, MaxConcurrentRuns: 1, MinRunInterval: time.Minute}) {
		t.Fatalf("unexpected budget: %+v", plan.Budget)
	}
	if !reflect.DeepEqual(binding.Prefixes, []string{"fe80::/64", "192.168.50.0/24"}) {
		t.Fatal("input mutated")
	}
	if !reflect.DeepEqual(plan.Binding.Prefixes, []string{"192.168.50.0/24", "fe80::/64"}) {
		t.Fatal("noncanonical plan")
	}
	binding.Prefixes[0] = "10.0.0.0/24"
	if plan.Binding.Prefixes[1] != "fe80::/64" {
		t.Fatal("plan aliases input")
	}
	plan.Binding.Prefixes[0] = "172.16.0.0/24"
	if binding.Prefixes[1] != "192.168.50.0/24" {
		t.Fatal("input aliases plan")
	}
}

func TestGatewayPreviewRejectsUnscopedAndAmbiguousDestinations(t *testing.T) {
	binding, now := gatewayPlanFixture()
	for _, target := range []string{"", "gateway.local", "https://192.168.50.1", "192.168.50.1:80", "192.168.50.1/24",
		"192.168.050.1", " 192.168.50.1", "192.168.50.1\n", "192.168.50.1%en0", "::ffff:192.168.50.1", "fe80::1%en0",
		"8.8.8.8", "127.0.0.1", "0.0.0.0", "255.255.255.255", "224.0.0.1", "169.254.169.254", "100.64.0.1",
		"192.0.2.1", "10.0.0.1", "192.168.50.0", "192.168.50.255", strings.Repeat("a", 65536)} {
		t.Run(target[:min(len(target), 40)], func(t *testing.T) {
			plan, err := PreviewGatewayCheck(binding, target, now)
			if !errors.Is(err, ErrGatewayPlanTarget) || !reflect.DeepEqual(plan, GatewayCheckPlan{}) {
				t.Fatalf("target accepted: %v", err)
			}
			if strings.Contains(err.Error(), target) && len(target) > 15 {
				t.Fatal("diagnostic echoed input")
			}
		})
	}
}

func TestGatewayPreviewPrefixMatrix(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prefixes []string
		target   string
		valid    bool
	}{
		{"private10", []string{"10.0.0.0/8"}, "10.0.0.1", true},
		{"private172", []string{"172.16.0.0/12"}, "172.31.255.254", true},
		{"private192", []string{"192.168.0.0/16"}, "192.168.255.254", true},
		{"v6-only", []string{"fd00::/64"}, "192.168.50.1", false},
		{"too-broad", []string{"192.0.0.0/8"}, "192.168.50.1", false},
		{"point-to-point-prefix", []string{"192.168.50.0/31"}, "192.168.50.1", false},
		{"host-prefix", []string{"192.168.50.1/32"}, "192.168.50.1", false},
		{"overlap-network", []string{"192.168.0.0/16", "192.168.50.0/24"}, "192.168.50.0", false},
		{"overlap-broadcast", []string{"192.168.0.0/16", "192.168.50.0/24"}, "192.168.50.255", false},
		{"overlap-safe", []string{"192.168.0.0/16", "192.168.50.0/24"}, "192.168.50.1", true},
		{"mapped-prefix", []string{"::ffff:192.168.50.0/120"}, "192.168.50.1", false},
		{"host-bits", []string{"192.168.50.1/24"}, "192.168.50.1", false},
		{"duplicate", []string{"192.168.50.0/24", "192.168.50.0/24"}, "192.168.50.1", false},
		{"bad-prefix", []string{"private-secret-prefix"}, "192.168.50.1", false},
		{"loopback", []string{"127.0.0.0/8"}, "192.168.50.1", false},
		{"multicast", []string{"224.0.0.0/4"}, "192.168.50.1", false},
		{"default-route", []string{"0.0.0.0/0"}, "192.168.50.1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding, now := gatewayPlanFixture()
			binding.Prefixes = tc.prefixes
			_, err := PreviewGatewayCheck(binding, tc.target, now)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestGatewayPreviewRejectsInvalidContext(t *testing.T) {
	for _, edit := range []func(*GatewayPlanBinding){
		func(b *GatewayPlanBinding) { b.ScopeID = "<script>" },
		func(b *GatewayPlanBinding) { b.InterfaceName = "en0;id" },
		func(b *GatewayPlanBinding) { b.InterfaceIndex = 0 },
		func(b *GatewayPlanBinding) { b.InterfaceIndex = 2147483648 },
		func(b *GatewayPlanBinding) { b.Prefixes = nil },
		func(b *GatewayPlanBinding) { b.Prefixes = make([]string, 33) },
		func(b *GatewayPlanBinding) { b.Prefixes = []string{strings.Repeat("a", 65)} },
	} {
		binding, now := gatewayPlanFixture()
		edit(&binding)
		if _, err := PreviewGatewayCheck(binding, "192.168.50.1", now); !errors.Is(err, ErrGatewayPlanBinding) {
			t.Fatalf("invalid binding: %v", err)
		}
	}
	binding, _ := gatewayPlanFixture()
	for _, now := range []time.Time{time.Time{}, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 23, 59, 45, 0, time.UTC)} {
		if _, err := PreviewGatewayCheck(binding, "192.168.50.1", now); !errors.Is(err, ErrGatewayPlanClock) {
			t.Fatalf("invalid clock: %v", err)
		}
	}
}
