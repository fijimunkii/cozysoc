package opnsense

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

func testTrust(server *httptest.Server) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
}

func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := NewClient(server.URL, "reader-key", secretstore.NewSecret([]byte("reader-secret")), testTrust(server))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestReadOnlyNeighborsUsesFixedGETsAndProjectsBoundedEvidence(t *testing.T) {
	var requests []string
	var mu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		key, secret, ok := r.BasicAuth()
		if !ok || key != "reader-key" || secret != "reader-secret" {
			t.Error("missing API credential")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.RequestURI() {
		case "/api/diagnostics/system/system_information":
			fmt.Fprint(w, `{"name":"private-router.local","versions":["OPNsense 26.7.4-amd64","FreeBSD 15.1"],"private":"not projected"}`)
		case "/api/diagnostics/interface/get_arp":
			fmt.Fprint(w, `[{"ip":"192.168.1.20","mac":"aa:bb:cc:dd:ee:ff","intf":"igb0.10","hostname":"private-child-device","manufacturer":"private vendor","intf_description":"private LAN"}]`)
		case "/api/diagnostics/interface/get_ndp":
			fmt.Fprint(w, `[{"ip":"fe80::aabb:ccff:fedd:eeff%igb0","mac":"aa:bb:cc:dd:ee:ff","intf":"igb0","manufacturer":"private vendor"}]`)
		default:
			t.Errorf("unexpected API path: %s", r.URL.RequestURI())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	snapshot, err := newTestClient(t, server).ReadNeighbors(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := strings.Join(requests, ",")
	mu.Unlock()
	if got != "GET /api/diagnostics/system/system_information,GET /api/diagnostics/interface/get_arp,GET /api/diagnostics/interface/get_ndp" {
		t.Fatalf("unexpected requests: %s", got)
	}
	if snapshot.Status.Version != SupportedVersion || snapshot.IPv4Total != 1 || snapshot.IPv6Total != 1 || len(snapshot.Neighbors) != 2 ||
		snapshot.Neighbors[0].Interface != "igb0.10" || snapshot.Neighbors[0].Family != "ipv4" ||
		snapshot.Neighbors[1].IP.String() != "fe80::aabb:ccff:fedd:eeff" || snapshot.Neighbors[1].Family != "ipv6" {
		t.Fatalf("wrong neighbor projection: %+v", snapshot)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{"private-router", "private-child-device", "private vendor", "private LAN", "reader-secret"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("private upstream field leaked: %s", forbidden)
		}
	}
}

func TestEndpointAndTLSBoundary(t *testing.T) {
	secret := secretstore.NewSecret([]byte("reader-secret"))
	for _, endpoint := range []string{"http://127.0.0.1", "https://router.local", "https://203.0.113.10", "https://user:pass@127.0.0.1", "https://127.0.0.1/path", "https://127.0.0.1?x=1", "https://127.0.0.1#fragment", "https://127.0.0.1:0", "https://127.0.0.1:65536"} {
		if _, err := NewClient(endpoint, "reader", secret, nil); !errors.Is(err, ErrEndpoint) {
			t.Errorf("accepted unsafe endpoint %q: %v", endpoint, err)
		}
	}
	if _, err := NewClient("https://127.0.0.1", "reader:key", secret, nil); !errors.Is(err, ErrEndpoint) {
		t.Fatal("accepted ambiguous API key")
	}
	if _, err := NewClient("https://127.0.0.1", "reader", secret, []byte("not a certificate")); !errors.Is(err, ErrTrust) {
		t.Fatalf("accepted invalid certificate trust: %v", err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"versions":["OPNsense 26.7.4-amd64"]}`)
	}))
	defer server.Close()
	untrusted, err := NewClient(server.URL, "reader", secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := untrusted.Probe(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("accepted untrusted certificate: %v", err)
	}
	trusted, err := NewClient(server.URL, "reader", secret, testTrust(server))
	if err != nil {
		t.Fatal(err)
	}
	if status, err := trusted.Probe(context.Background()); err != nil || status.Version != SupportedVersion {
		t.Fatalf("explicit trust failed: %+v %v", status, err)
	}
}

func TestProbeRejectsWrongVersionWithoutReadingNeighbors(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"versions":["OPNsense 26.7.1-amd64"]}`)
	}))
	defer server.Close()
	if _, err := newTestClient(t, server).ReadNeighbors(context.Background()); !errors.Is(err, ErrVersion) || reads.Load() != 1 {
		t.Fatalf("unsupported version reached neighbor paths: %v, %d reads", err, reads.Load())
	}
}

func TestReadBoundsAndMalformedRowsFailClosed(t *testing.T) {
	rows := make([]map[string]string, MaxNeighbors+1)
	for i := range rows {
		rows[i] = map[string]string{"ip": fmt.Sprintf("192.168.%d.%d", i/256, i%256), "mac": "aa:bb:cc:dd:ee:ff", "intf": "igb0"}
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var malformed atomic.Value
	malformed.Store("")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/diagnostics/system/system_information":
			fmt.Fprint(w, `{"versions":["OPNsense 26.7.4-amd64"]}`)
		case "/api/diagnostics/interface/get_arp":
			if bad := malformed.Load().(string); bad != "" {
				fmt.Fprint(w, bad)
			} else {
				_, _ = w.Write(encoded)
			}
		case "/api/diagnostics/interface/get_ndp":
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server)
	snapshot, err := client.ReadNeighbors(context.Background())
	if err != nil || snapshot.IPv4Total != MaxNeighbors+1 || !snapshot.IPv4Truncated || len(snapshot.Neighbors) != MaxNeighbors {
		t.Fatalf("wrong bounded result: %+v, %v", snapshot, err)
	}
	for _, bad := range []string{
		`[{"ip":"192.168.1.3","mac":"aa:bb:cc:dd:ee:ff","intf":"igb0\u202ename"}]`,
		`[{"ip":"192.168.1.3","mac":"00:00:00:00:00:00","intf":"igb0"}]`,
		`[{"ip":"192.168.1.3","mac":"ff:ff:ff:ff:ff:ff","intf":"igb0"}]`,
		`[{"ip":"::ffff:192.168.1.3","mac":"aa:bb:cc:dd:ee:ff","intf":"igb0"}]`,
	} {
		malformed.Store(bad)
		if _, err := client.ReadNeighbors(context.Background()); !errors.Is(err, ErrResponse) {
			t.Fatalf("malformed neighbor accepted: %v", err)
		}
	}
}

func TestAuthAndRedirectDoNotLeakOrFollow(t *testing.T) {
	var redirected atomic.Bool
	other := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Store(true) }))
	defer other.Close()
	var status atomic.Int32
	status.Store(http.StatusForbidden)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status.Load() == http.StatusFound {
			w.Header().Set("Location", other.URL)
		}
		w.WriteHeader(int(status.Load()))
		fmt.Fprint(w, "private API diagnostic and credential")
	}))
	defer server.Close()
	client := newTestClient(t, server)
	if _, err := client.Probe(context.Background()); !errors.Is(err, ErrAuth) || strings.Contains(err.Error(), "private") {
		t.Fatalf("authentication error exposed body: %v", err)
	}
	status.Store(http.StatusFound)
	if _, err := client.Probe(context.Background()); !errors.Is(err, ErrUnavailable) || redirected.Load() {
		t.Fatalf("redirect followed: %v, %v", err, redirected.Load())
	}
}
