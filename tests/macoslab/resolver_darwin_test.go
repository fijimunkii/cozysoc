package macoslab

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverroute"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverudp"
)

type dnsAudit struct{ events []resolverrun.Event }

func (a *dnsAudit) InsertResolverRunAudit(_ context.Context, e resolverrun.Event) error {
	if err := resolverrun.ValidateEvent(e); err != nil {
		return err
	}
	a.events = append(a.events, e)
	return nil
}
func runDNSLab(t *testing.T, mode string) {
	r := labRequest(t)
	b := r.Plan.Binding
	enrollment := resolverroute.Enrollment{Observer: nq.Observer{ScopeID: b.ScopeID, SensorID: "fixture", InterfaceName: b.InterfaceName, InterfaceIndex: b.InterfaceIndex}, Prefixes: b.Prefixes}
	config := resolverplan.Configuration{Selection: nq.ResolverSelection{ID: "dns1", ResolverID: "resolver-v1", QueryID: "query-v1", Family: nq.FamilyIPv4, Transport: nq.DNSUDP, QueryType: nq.DNSQueryA, Expect: nq.DNSExpectAnswer}, Endpoint: netip.AddrPortFrom(r.Plan.Target, 53), Name: "test.example.", DestinationScope: resolverplan.EnrolledPrefix}
	peerMode := mode
	if mode == "dns-cancel" || mode == "dns-source-loss" {
		peerMode = "dns-silent"
	}
	p := startPeer(t, peerMode)
	constructed := false
	audit := &dnsAudit{}
	c, err := resolverrun.New(resolverrun.Dependencies{Now: func() time.Time {
		if !constructed {
			return time.Now().Add(-resolverrun.RunInterval)
		}
		return time.Now()
	}, Auditor: audit, Executor: resolverudp.NewCandidate(), Preflight: func(ctx context.Context, _ string) (resolverrun.Selection, error) {
		return resolverroute.NewInspector().Inspect(ctx, enrollment, config)
	}})
	if err != nil {
		t.Fatal(err)
	}
	constructed = true
	t.Cleanup(func() {
		if err := c.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	review, err := c.Prepare(ctx, "dns1")
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.events) != 0 {
		t.Fatal("review recorded consent")
	}
	var result resolverrun.Result
	if mode == "dns-cancel" || mode == "dns-source-loss" {
		done := make(chan struct{})
		go func() { result, err = c.Run(ctx, review.Ticket, true); close(done) }()
		select {
		case <-p.first:
		case <-time.After(5 * time.Second):
			cancel()
			<-done
			t.Fatal("DNS request not observed")
		}
		if mode == "dns-cancel" {
			cancel()
		} else {
			fixtureIfconfig(t, "inet", source, "-alias")
			defer fixtureIfconfig(t, "inet", source+"/24", "alias")
		}
		<-done
	} else {
		result, err = c.Run(ctx, review.Ticket, true)
	}
	p.stop(t)
	queries, summaries := 0, 0
	for _, e := range p.events {
		switch e.Event {
		case "ready":
		case "query":
			queries++
			if e.Bytes != 30 {
				t.Fatal("unexpected DNS size")
			}
		case "summary":
			summaries++
			if e.Queries != 1 || e.Echoes != 0 {
				t.Fatal("unexpected packet count")
			}
		default:
			t.Fatal("unexpected peer evidence")
		}
	}
	if queries != 1 || summaries != 1 {
		t.Fatal("missing DNS wire evidence")
	}
	if len(audit.events) != 3 || audit.events[0].State != "authorized" || audit.events[1].State != "admitted" || audit.events[2].State != "finished" || result.Sample == nil || audit.events[2].Measurement == nil {
		t.Fatalf("missing audited DNS result: %+v %v", result, err)
	}
	m := result.Sample
	if m.Request != nq.DNSRequestAccepted {
		t.Fatal("kernel accepted query not recorded")
	}
	switch mode {
	case "dns-answer":
		if err != nil || m.Exchange != nq.DNSResponseReceived || m.Reply.Answer != nq.DNSAnswerPresent {
			t.Fatalf("answer: %+v %v", m, err)
		}
	case "dns-nxdomain":
		if err != nil || m.Exchange != nq.DNSResponseReceived || m.Reply.RCode != 3 {
			t.Fatalf("NXDOMAIN: %+v %v", m, err)
		}
	case "dns-silent", "dns-wrong-id":
		if err != nil || m.Exchange != nq.DNSTimeout || m.Reply != nil {
			t.Fatalf("timeout: %+v %v", m, err)
		}
	case "dns-cancel":
		if !errors.Is(err, context.Canceled) || m.Exchange != nq.DNSIncomplete {
			t.Fatalf("cancel: %+v %v", m, err)
		}
	case "dns-source-loss":
		if err == nil || m.Exchange != nq.DNSIncomplete {
			t.Fatalf("source loss: %+v %v", m, err)
		}
	}
	if _, err := c.Run(context.Background(), review.Ticket, true); !errors.Is(err, resolverrun.ErrReview) {
		t.Fatal("DNS ticket replayed", err)
	}
}
