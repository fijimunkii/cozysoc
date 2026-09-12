package localapi

import (
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

func TestResolverReviewRejectsChangedDisclosure(t *testing.T) {
	s, _, _, _, _, _ := dnsSessionFixture(t, nil, nil, false)
	conn, _, r := openDNSReview(t, s)
	conn.Close()
	for _, change := range []func(*api.ResolverCheckReview){
		func(r *api.ResolverCheckReview) { r.Mode = "preview-only" }, func(r *api.ResolverCheckReview) { r.Profile = "other" },
		func(r *api.ResolverCheckReview) { r.Settings.Endpoint = "127.0.0.1:53" }, func(r *api.ResolverCheckReview) { r.Settings.Name = "bad\x1b.example." },
		func(r *api.ResolverCheckReview) { r.Settings.Name = "Test.Example." }, func(r *api.ResolverCheckReview) { r.Settings.Transport = "tcp" },
		func(r *api.ResolverCheckReview) { r.Settings.Family = "ipv6" }, func(r *api.ResolverCheckReview) { r.Settings.Expect = "" },
		func(r *api.ResolverCheckReview) { r.Source = "192.168.50.1" }, func(r *api.ResolverCheckReview) { r.Binding.InterfaceIndex = 0 },
		func(r *api.ResolverCheckReview) { r.MayForwardUpstream = false }, func(r *api.ResolverCheckReview) { r.OutsideEnrolledPrefixes = true },
		func(r *api.ResolverCheckReview) { r.Budget.MaxSendCalls++ }, func(r *api.ResolverCheckReview) { r.Budget.MaxRequestBytes++ },
		func(r *api.ResolverCheckReview) { r.ExpiresAt = r.CreatedAt.Add(time.Hour) }, func(r *api.ResolverCheckReview) { r.Challenge = "bad" },
	} {
		changed := cloneResolverReview(r)
		change(&changed)
		if validateResolverReview(changed, r.SelectionID, time.Now()) == nil {
			t.Fatal("invalid disclosure accepted")
		}
	}
}
func TestResolverResultSemantics(t *testing.T) {
	s, _, _, _, _, _ := dnsSessionFixture(t, nil, nil, false)
	conn, _, r := openDNSReview(t, s)
	conn.Close()
	zero := int64(0)
	m := &api.ResolverRunMeasurement{StartedAt: r.CreatedAt, CompletedAt: r.CreatedAt.Add(time.Millisecond), Request: nq.DNSRequestAccepted, Exchange: nq.DNSResponseReceived, Reply: &nq.DNSReply{RCode: 3}, ResponseTimeNanoseconds: &zero}
	result := api.ResolverCheckResult{SchemaVersion: 1, Review: r, RunID: strings.Repeat("b", 32), Outcome: "completed", Measurement: m}
	now := r.CreatedAt.Add(time.Second)
	if validateResolverResult(result, r, now) != nil {
		t.Fatal("measured NXDOMAIN rejected")
	}
	// A normalized response can survive failed socket cleanup; completed protocol
	// or matched DNS response must not turn the execution outcome into success.
	result.Outcome = "failed"
	result.FailureCode = "execution_failed"
	if validateResolverResult(result, r, now) != nil {
		t.Fatal("failed cleanup discarded matched response")
	}
	m.StartedAt = r.ExpiresAt.Add(-time.Millisecond)
	m.CompletedAt = r.ExpiresAt.Add(time.Millisecond)
	m.Exchange = nq.DNSIncomplete
	m.Reply = nil
	m.ResponseTimeNanoseconds = nil
	if validateResolverResult(result, r, r.ExpiresAt.Add(time.Second)) != nil {
		t.Fatal("late incomplete cleanup rejected")
	}
	m.Exchange = nq.DNSResponseReceived
	m.Reply = &nq.DNSReply{RCode: 3}
	m.ResponseTimeNanoseconds = &zero
	if validateResolverResult(result, r, r.ExpiresAt.Add(time.Second)) == nil {
		t.Fatal("late complete response accepted")
	}
}
