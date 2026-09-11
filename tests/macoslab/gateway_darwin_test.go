package macoslab

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/gatewayicmp"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayroute"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

const source = "192.168.250.2"
const target = "192.168.250.1"

func labRequest(t *testing.T) gatewayicmp.Request {
	t.Helper()
	iface, err := net.InterfaceByName("feth42")
	if err != nil {
		t.Fatal(err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		t.Fatal(err)
	}
	prefixes := map[string]bool{}
	found := false
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a.String())
		if err != nil {
			t.Fatal(err)
		}
		if p.Addr().String() == source {
			found = true
		}
		m := p.Masked()
		if !m.Addr().IsUnspecified() && !m.Addr().IsLoopback() && !m.Addr().IsMulticast() {
			prefixes[m.String()] = true
		}
	}
	if !found {
		t.Fatal("isolated source missing")
	}
	list := []string{}
	for p := range prefixes {
		list = append(list, p)
	}
	sort.Strings(list)
	plan, err := networkquality.PreviewGatewayCheck(networkquality.GatewayPlanBinding{
		ScopeID: "scope.ci-macos", InterfaceName: iface.Name, InterfaceIndex: iface.Index, Prefixes: list,
	}, target, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return gatewayicmp.Request{Plan: plan, Source: netip.MustParseAddr(source)}
}

type peerEvent struct {
	Event  string `json:"event"`
	UID    int    `json:"uid"`
	Seq    int    `json:"seq"`
	Bytes  int    `json:"bytes"`
	AtUS   int64  `json:"at_us"`
	Echoes int    `json:"echoes"`
}
type peer struct {
	input  io.WriteCloser
	cancel context.CancelFunc
	ready  chan peerEvent
	first  chan struct{}
	done   chan struct{}
	events []peerEvent // Read only after done closes.
	err    error
	stderr bytes.Buffer
	once   sync.Once
}

func startPeer(t *testing.T, mode string) *peer {
	t.Helper()
	path := os.Getenv("COZYSOC_LAB_PEER")
	if !filepath.IsAbs(path) {
		t.Fatal("absolute lab peer path required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	p := &peer{cancel: cancel, ready: make(chan peerEvent, 1), first: make(chan struct{}), done: make(chan struct{})}
	cmd := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", path, mode)
	cmd.Stderr = &p.stderr
	var err error
	p.input, err = cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	go func() {
		defer close(p.done)
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 1024), 2048)
		first := false
		for scanner.Scan() {
			var e peerEvent
			if len(p.events) >= 70 || json.Unmarshal(scanner.Bytes(), &e) != nil {
				p.err = errors.New("invalid or excessive peer output")
				cancel()
				break
			}
			p.events = append(p.events, e)
			if e.Event == "ready" {
				select {
				case p.ready <- e:
				default:
				}
			}
			if e.Event == "echo" && !first {
				first = true
				close(p.first)
			}
		}
		if err := scanner.Err(); err != nil {
			p.err = err
		}
		if err := cmd.Wait(); err != nil {
			p.err = err
		}
	}()
	t.Cleanup(func() { p.stop(t) })
	select {
	case e := <-p.ready:
		if e.UID != os.Geteuid() || e.UID == 0 {
			t.Fatal("peer failed to drop privileges")
		}
	case <-p.done:
		t.Fatalf("peer setup: %v %s", p.err, p.stderr.String())
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("peer setup timed out")
	}
	return p
}
func (p *peer) stop(t *testing.T) {
	t.Helper()
	p.once.Do(func() {
		_ = p.input.Close()
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			p.cancel()
			<-p.done
		}
		p.cancel()
		if p.err != nil {
			t.Errorf("peer: %v %s", p.err, p.stderr.String())
		}
	})
}
func (p *peer) assertEchoes(t *testing.T, want int) {
	t.Helper()
	p.stop(t)
	count, summaries := 0, 0
	var previous int64
	for _, e := range p.events {
		switch e.Event {
		case "ready":
		case "echo":
			count++
			if e.Seq != count || e.Bytes != 40 || e.AtUS <= 0 {
				t.Fatal("unexpected observed echo")
			}
			// The sender enforces >=1s; capture timestamps have scheduling jitter.
			if previous != 0 && e.AtUS-previous < 950000 {
				t.Fatal("observed a request burst")
			}
			previous = e.AtUS
		case "summary":
			summaries++
			if e.Echoes != want {
				t.Fatalf("peer observed %d echoes, want %d", e.Echoes, want)
			}
		default:
			t.Fatal("unknown peer event")
		}
	}
	if count != want || summaries != 1 {
		t.Fatalf("missing peer evidence: count=%d summaries=%d", count, summaries)
	}
}
func fixtureIfconfig(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	all := append([]string{"-n", "/sbin/ifconfig", "feth42"}, args...)
	if out, err := exec.CommandContext(ctx, "/usr/bin/sudo", all...).CombinedOutput(); err != nil {
		t.Fatalf("fixture change: %v %s", err, out)
	}
}

func TestMACOSGatewayLab(t *testing.T) {
	if os.Getenv("COZYSOC_MACOS_LAB") != "1" {
		t.Skip("explicit isolated lab setup required")
	}
	if os.Geteuid() == 0 {
		t.Fatal("Cozy SOC tests must run unprivileged")
	}
	t.Run("route", func(t *testing.T) {
		r := labRequest(t)
		e, err := gatewayroute.NewInspector().Inspect(context.Background(), r.Plan.Binding, r.Plan.Target)
		if err != nil || e.SourceAddress != r.Source || e.InterfaceIndex != r.Plan.Binding.InterfaceIndex {
			t.Fatalf("real route: %+v %v", e, err)
		}
		wrong := r.Plan.Binding
		wrong.InterfaceIndex++
		if _, err := gatewayroute.NewInspector().Inspect(context.Background(), wrong, r.Plan.Target); err == nil {
			t.Fatal("wrong binding accepted")
		}
		if _, err := gatewayroute.NewInspector().Inspect(context.Background(), r.Plan.Binding, r.Source); err == nil {
			t.Fatal("self-target accepted")
		}
	})
	for _, mode := range []string{"reply", "silent", "wrong-nonce"} {
		t.Run(mode, func(t *testing.T) {
			p := startPeer(t, mode)
			sample, err := coordinatedSample(t, context.Background(), labRequest(t))
			p.assertEchoes(t, 3)
			if err != nil || !sample.Complete || sample.SendCalls != 3 || sample.AcceptedRequests != 3 {
				t.Fatalf("real sample: %+v %v", sample, err)
			}
			want := 0
			if mode == "reply" {
				want = 3
			}
			if sample.Replies != want || sample.Timeouts != 3-want || (sample.MeanRTT == nil) != (want == 0) {
				t.Fatalf("incorrect measured outcome: %+v", sample)
			}
			if sample.Target.String() != target || sample.Source.String() != source || sample.InterfaceName != "feth42" || sample.CompletedAt.Before(sample.StartedAt) {
				t.Fatal("lost measurement provenance")
			}
		})
	}
	for _, mode := range []string{"cancel", "source-loss"} {
		t.Run(mode, func(t *testing.T) {
			p := startPeer(t, "reply")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := labRequest(t)
			run := prepareCoordinatedRun(t, ctx, r)
			type result struct {
				run gatewayrun.Result
				err error
			}
			done := make(chan result, 1)
			go func() {
				resultRun, err := run.control.Run(ctx, run.review.Ticket, true)
				done <- result{resultRun, err}
			}()
			select {
			case <-p.first:
			case got := <-done:
				t.Fatalf("no first echo: %+v %v", got.run, got.err)
			case <-time.After(6 * time.Second):
				t.Fatal("no first echo observed")
			}
			if mode == "cancel" {
				cancel()
			} else {
				fixtureIfconfig(t, "inet", "192.168.250.3/24", "alias")
				t.Cleanup(func() {
					fixtureIfconfig(t, "inet", "192.168.250.2/24", "alias")
					fixtureIfconfig(t, "inet", "192.168.250.3", "-alias")
				})
				fixtureIfconfig(t, "inet", "192.168.250.2", "-alias")
			}
			select {
			case got := <-done:
				sample := run.sample(t, got.run, got.err)
				if got.err == nil || sample.Complete || sample.MeanRTT != nil || sample.SendCalls != 1 {
					t.Fatalf("continued after change: %+v %v", sample, got.err)
				}
				if mode == "cancel" && !errors.Is(got.err, context.Canceled) {
					t.Fatal(got.err)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("sender did not stop")
			}
			time.Sleep(1100 * time.Millisecond) // Peer checks no queued second request follows.
			p.assertEchoes(t, 1)
		})
	}
}
