package macoslab

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsroute"
	"github.com/fijimunkii/cozysoc/internal/controller/httpstcp"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

// Real native route/TCP binding and TLS handshake through a restricted userspace
// peer. The ephemeral self-signed identity MUST be rejected by production trust.
func runHTTPSNativeLab(t *testing.T)  { runHTTPSNativeCase(t, false) }
func runHTTPSTrustedLab(t *testing.T) { runHTTPSNativeCase(t, true) }
func runHTTPSNativeCase(t *testing.T, trusted bool) {
	work := filepath.Dir(os.Getenv("COZYSOC_LAB_PEER"))
	listener, err := net.Listen("unix", filepath.Join(work, "https.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(filepath.Join(work, "https.sock"), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	name := "test.example"
	if trusted {
		name = fmt.Sprintf("cozysoc-%x.invalid", now.UnixNano())
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	rawKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: rawKey}))
	if err != nil {
		t.Fatal(err)
	}
	if trusted {
		trustHTTPSFixture(t, work, der, name)
	}
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		server := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})

		if err := server.Handshake(); err != nil || !trusted {
			done <- err
			return
		}
		request, err := http.ReadRequest(bufio.NewReader(server))
		if err != nil {
			done <- err
			return
		}
		if request.Method != "HEAD" || request.URL.RequestURI() != "/check" || request.Host != name || request.Header.Get("User-Agent") != "CozySOC-Network-Check/1" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			done <- fmt.Errorf("unexpected owned request")
			return
		}
		_, err = server.Write([]byte("HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n"))
		done <- err
	}()
	defer func() {
		listener.Close()
		select {
		case <-done:
		case <-time.After(9 * time.Second):
			t.Error("TLS fixture did not drain")
		}
	}()
	peer := startPeer(t, "https")
	r := labRequest(t)
	b := r.Plan.Binding
	e := httpsroute.Enrollment{Observer: nq.Observer{ScopeID: b.ScopeID, SensorID: "fixture", InterfaceName: b.InterfaceName, InterfaceIndex: b.InterfaceIndex}, Prefixes: b.Prefixes}
	config := httpsplan.Configuration{Selection: nq.HTTPSSelection{ID: "https1", EndpointID: "endpoint1", RequestID: "request1", Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 204}, Endpoint: netip.AddrPortFrom(r.Plan.Target, 443), ServerName: name, RequestTarget: "/check", DestinationPolicy: httpsplan.ExactEndpoint}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	selected, err := httpsroute.NewInspector().Inspect(ctx, e, config)
	if err != nil {
		t.Fatal(err)
	}
	m, err := httpstcp.NewCandidate().ExecuteHTTPS(ctx, httpstcp.Request{Selection: selected, MeasurementID: strings.Repeat("a", 32)})
	if trusted {
		if err != nil || m.Stage != nq.HTTPSRequest || m.Exchange != nq.HTTPSResponseReceived || m.Request != nq.HTTPSRequestAccepted || m.StatusCode != 204 || m.ResponseTime == nil || m.Observer != e.Observer {
			t.Fatalf("native trusted response: %+v %v", m, err)
		}
	} else if err == nil || m.Stage != nq.HTTPSTLS || m.Exchange != nq.HTTPSTLSError || m.Request != nq.HTTPSRequestNotSent || m.StatusCode != 0 || m.ResponseTime != nil || m.Observer != e.Observer {
		t.Fatalf("native TLS rejection: %+v %v", m, err)
	}
	select {
	case err := <-done:
		if trusted && err != nil {
			t.Fatal("owned HTTPS exchange failed", err)
		}
		if !trusted && err == nil {
			t.Fatal("untrusted TLS session accepted")
		}
		done <- err // Retain completion for deferred join.
	case <-time.After(2 * time.Second):
		t.Fatal("owned TLS server saw no rejected handshake")
	}
	peer.stop(t)
}
