package httpsroute

import (
	"context"
	"errors"
	"net/netip"
	"runtime"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/checkroute"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

type metadataFunc func(context.Context, checkroute.Enrollment, netip.Addr) (checkroute.Evidence, error)

func (f metadataFunc) Inspect(ctx context.Context, e checkroute.Enrollment, a netip.Addr) (checkroute.Evidence, error) {
	return f(ctx, e, a)
}
func fixture() (Enrollment, httpsplan.Configuration) {
	return Enrollment{Observer: nq.Observer{ScopeID: "home", SensorID: "local", InterfaceName: "fixture0", InterfaceIndex: 7}, Prefixes: []string{"192.0.2.0/24"}}, httpsplan.Configuration{Selection: nq.HTTPSSelection{ID: "https1", EndpointID: "endpoint1", RequestID: "request1", Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 204}, Endpoint: netip.MustParseAddrPort("198.51.100.53:443"), ServerName: "test.example", RequestTarget: "/check", DestinationPolicy: httpsplan.ExactEndpoint}
}
func TestReviewPreservesSelectionAndRouteAge(t *testing.T) {
	e, c := fixture()
	observed := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	completed := observed.Add(time.Second)
	calls := 0
	i := inspector{metadata: metadataFunc(func(ctx context.Context, got Enrollment, target netip.Addr) (checkroute.Evidence, error) {
		calls++
		if got.Observer != e.Observer || target != c.Endpoint.Addr() {
			t.Fatal("changed lookup binding")
		}
		return checkroute.Evidence{Source: netip.MustParseAddr("192.0.2.10"), ObservedAt: observed, FreshUntil: observed.Add(checkroute.Freshness), CompletedAt: completed}, nil
	})}
	selected, err := i.Inspect(context.Background(), e, c)
	if err != nil {
		t.Fatal(err)
	}
	d := selected.Plan.Disclosure()
	if httpsplan.Configuration(d.Configuration) != c || calls != 1 || d.Binding.Source != netip.MustParseAddr("192.0.2.10") || d.Binding.Observer != e.Observer || !d.CreatedAt.Equal(completed) || !selected.RouteObservedAt.Equal(observed) || !selected.RouteFreshUntil.Equal(observed.Add(checkroute.Freshness)) {
		t.Fatal("lost selection or refreshed route age")
	}
	e.Prefixes[0] = "198.51.100.0/24"
	if selected.Plan.Disclosure().Binding.Prefixes[0] != "192.0.2.0/24" {
		t.Fatal("review aliases caller enrollment")
	}
}
func TestInvalidConfigurationAndCancellationDoNotInspect(t *testing.T) {
	e, c := fixture()
	calls := 0
	i := inspector{metadata: metadataFunc(func(context.Context, Enrollment, netip.Addr) (checkroute.Evidence, error) {
		calls++
		return checkroute.Evidence{}, ErrUnavailable
	})}
	bad := c
	c.DestinationPolicy = ""
	if _, err := i.Inspect(context.Background(), e, c); !errors.Is(err, ErrMismatch) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := i.Inspect(ctx, e, bad); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("invalid request inspected metadata")
	}
	for _, want := range []error{ErrUnavailable, ErrPermission, ErrNoRoute, ErrMismatch, ErrUnsupported} {
		i.metadata = metadataFunc(func(context.Context, Enrollment, netip.Addr) (checkroute.Evidence, error) {
			return checkroute.Evidence{}, want
		})
		if _, err := i.Inspect(context.Background(), e, bad); !errors.Is(err, want) {
			t.Fatal("lost route failure", err)
		}
	}
}
func TestUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("non-Darwin behavior")
	}
	e, c := fixture()
	if _, err := NewInspector().Inspect(context.Background(), e, c); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}
