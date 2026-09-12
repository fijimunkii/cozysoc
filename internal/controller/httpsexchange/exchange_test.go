package httpsexchange

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

func testPlan(t *testing.T) httpsplan.Plan {
	t.Helper()
	p, err := httpsplan.New(httpsplan.Binding{Observer: nq.Observer{ScopeID: "fixture", SensorID: "local", InterfaceName: "fixture0", InterfaceIndex: 7}, Prefixes: []string{"192.0.2.0/24"}, Source: netip.MustParseAddr("192.0.2.10")}, httpsplan.Configuration{Selection: nq.HTTPSSelection{ID: "https1", EndpointID: "endpoint1", RequestID: "request1", Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 204}, Endpoint: netip.MustParseAddrPort("198.51.100.20:443"), ServerName: "test.example", RequestTarget: "/check?test=1", DestinationPolicy: httpsplan.ExactEndpoint}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func certificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test.example"}, DNSNames: []string{"test.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, roots
}
func peer(t *testing.T, config *tls.Config, reply string) (net.Conn, <-chan *http.Request) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
		close(accepted)
	}()
	client, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	server := <-accepted
	listener.Close()
	if server == nil {
		client.Close()
		t.Fatal("TLS fixture accept failed")
	}
	requests := make(chan *http.Request, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(requests)
		defer server.Close()
		_ = server.SetDeadline(time.Now().Add(5 * time.Second))
		s := tls.Server(server, config)
		if s.Handshake() != nil {
			return
		}
		r, err := http.ReadRequest(bufio.NewReader(s))
		if err != nil {
			return
		}
		requests <- r
		if reply == "__stall__" {
			var one [1]byte
			_, _ = s.Read(one[:])
			return
		}
		_, _ = io.WriteString(s, reply)
	}()
	t.Cleanup(func() {
		client.Close()
		server.Close()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Error("TLS peer did not stop")
		}
	})
	return client, requests
}
func serverConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS13, SessionTicketsDisabled: true}
}
func TestTLSExchangeResponseSemanticsAndExactRequest(t *testing.T) {
	cert, roots := certificate(t)
	for _, tc := range []struct {
		name, reply string
		status      int
		want        error
	}{
		{"expected", "HTTP/1.1 204 No Content\r\n\r\n", 204, nil},
		{"redirect", "HTTP/1.1 302 Found\r\nLocation: https://other.example/private\r\nContent-Length: 900000\r\n\r\n", 302, nil},
		{"server-error", "HTTP/1.1 503 Unavailable\r\nContent-Length: 999999\r\n\r\n", 503, nil},
		{"informational", "HTTP/1.1 103 Early Hints\r\nLink: </private>\r\n\r\nHTTP/1.1 204 No Content\r\n\r\n", 204, nil},
		{"connection-lost", "", 0, ErrTransport},
		{"upgrade", "HTTP/1.1 101 Switching Protocols\r\n\r\n", 0, ErrProtocol},
		{"http10", "HTTP/1.0 204 No Content\r\n\r\n", 0, ErrProtocol},
		{"invalid-status", "HTTP/1.1 999 Invalid\r\n\r\n", 0, ErrProtocol},
		{"bare-lf", "HTTP/1.1 204 No Content\n\n", 0, ErrProtocol},
		{"conflicting-lengths", "HTTP/1.1 200 OK\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\n", 0, ErrProtocol},
		{"oversized-headers", "HTTP/1.1 200 OK\r\nX-Large: " + strings.Repeat("x", 17000) + "\r\n\r\n", 0, ErrBudget},
		{"informational-budget", strings.Repeat("HTTP/1.1 100 Continue\r\n\r\n", 800), 0, ErrBudget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, requests := peer(t, serverConfig(cert), tc.reply)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := exchange(ctx, testPlan(t), conn, roots)
			if !errors.Is(err, tc.want) || result.StatusCode != tc.status || result.Stage != nq.HTTPSRequest || result.Request != nq.HTTPSRequestAccepted {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if tc.want == nil && result.Exchange != nq.HTTPSResponseReceived {
				t.Fatal("lost response")
			}
			if tc.want == ErrProtocol && result.Exchange != nq.HTTPSProtocolError {
				t.Fatal("lost protocol failure")
			}
			if result.ResponseHeaderBytes > 16384 || result.TransportReadBytes > 131072 || result.TransportWriteBytes > 32768 || result.TransportReadCalls > 512 || result.TransportWriteCalls > 64 {
				t.Fatal("exceeded reviewed budget")
			}
			request := <-requests
			if request == nil || request.Method != "HEAD" || request.URL.RequestURI() != "/check?test=1" || request.Host != "test.example" || request.Header.Get("User-Agent") != httpsplan.UserAgent || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || !request.Close {
				t.Fatal("request differs from review")
			}
		})
	}
}
func TestTLSIdentityAndALPNBeforeRequest(t *testing.T) {
	cert, roots := certificate(t)
	for _, mode := range []string{"untrusted", "wrong-name", "no-alpn", "wrong-alpn", "tls12", "tls13"} {
		t.Run(mode, func(t *testing.T) {
			config := serverConfig(cert)
			pool := roots
			p := testPlan(t)
			switch mode {
			case "untrusted":
				pool = nil
			case "wrong-name":
				d := p.Disclosure()
				c := httpsplan.Configuration(d.Configuration)
				c.ServerName = "other.example"
				p, _ = httpsplan.New(d.Binding, c, time.Now())
			case "no-alpn":
				config.NextProtos = nil
			case "wrong-alpn":
				config.NextProtos = []string{"h2"}
			case "tls12":
				config.MaxVersion = tls.VersionTLS12
			case "tls13":
				config.MinVersion = tls.VersionTLS13
			}
			conn, requests := peer(t, config, "HTTP/1.1 204 No Content\r\n\r\n")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			r, err := exchange(ctx, p, conn, pool)
			if mode == "tls12" || mode == "tls13" {
				if err != nil || r.StatusCode != 204 {
					t.Fatal(r, err)
				}
				return
			}
			if !errors.Is(err, ErrTLS) || r.Stage != nq.HTTPSTLS || r.Request != nq.HTTPSRequestNotSent || r.StatusCode != 0 {
				t.Fatal(r, err)
			}
			if request := <-requests; request != nil {
				t.Fatal("request sent before verified TLS")
			}
		})
	}
}
func TestCancellationAndHandshakeDeadlineCloseConnection(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout", "missing-deadline", "zero-plan"} {
		t.Run(mode, func(t *testing.T) {
			conn, server := net.Pipe()
			defer server.Close()
			p := testPlan(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			if mode == "missing-deadline" {
				ctx = context.Background()
			}
			if mode == "zero-plan" {
				p = httpsplan.Plan{}
			}
			if mode == "cancel" {
				cancel()
			}
			start := time.Now()
			r, err := Exchange(ctx, p, conn)
			if err == nil || time.Since(start) > time.Second || r.StatusCode != 0 {
				t.Fatal("unbounded or successful canceled exchange", r, err)
			}
			if mode == "timeout" && (err != context.DeadlineExceeded || r.Exchange != nq.HTTPSTimeout) {
				t.Fatal("lost timeout", r, err)
			}
			_ = server.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := server.Read(make([]byte, 1)); err == nil {
				t.Fatal("connection remained open")
			}
		})
	}
}

func TestHeaderDeadlineAfterVerifiedTLS(t *testing.T) {
	cert, roots := certificate(t)
	conn, _ := peer(t, serverConfig(cert), "__stall__")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	r, err := exchange(ctx, testPlan(t), conn, roots)
	if !errors.Is(err, context.DeadlineExceeded) || r.Stage != nq.HTTPSRequest || r.Request != nq.HTTPSRequestAccepted || r.Exchange != nq.HTTPSTimeout || r.StatusCode != 0 {
		t.Fatal("lost header-stage timeout", r, err)
	}
}
