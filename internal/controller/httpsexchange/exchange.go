// Package httpsexchange implements one bounded TLS/HTTP exchange on a connection
// already verified by trusted native run control. It never dials, resolves names,
// creates consent, follows redirects, or enables product execution.
package httpsexchange

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

var (
	ErrUnavailable = errors.New("HTTPS exchange unavailable")
	ErrBudget      = errors.New("HTTPS exchange budget exhausted")
	ErrTLS         = errors.New("HTTPS identity or handshake failed")
	ErrProtocol    = errors.New("HTTPS response protocol invalid")
	ErrTransport   = errors.New("HTTPS transport failed")
)

// Result omits endpoint, request, headers, bodies, certificates and raw errors.
// Exchange begins timing after connection establishment. The TCP candidate
// retains the earlier connect start and final attribution completion separately.
type Result struct {
	// ResponseReceivedAt is captured at final header receipt, before parsing and
	// later route validation. Zero means no attributable final response.
	StartedAt, CompletedAt, ResponseReceivedAt time.Time
	Stage                                      nq.HTTPSStage
	Exchange                                   nq.HTTPSExchange
	Request                                    nq.HTTPSRequestState
	StatusCode                                 int
	TransportReadBytes, TransportWriteBytes    int
	TransportReadCalls, TransportWriteCalls    int
	ResponseHeaderBytes                        int
}

// Exchange takes ownership of conn, closing it on every path. The caller must
// have consumed one-shot consent, verified route and actual socket binding, and
// supplied the original absolute run deadline. A plan is not execution authority.
// This component does not enforce controller-wide admission/cooldown by itself.
func Exchange(ctx context.Context, plan httpsplan.Plan, conn net.Conn) (Result, error) {
	return exchange(ctx, plan, conn, nil)
}

// roots is test-only injection; production always uses the system trust store.
func exchange(ctx context.Context, plan httpsplan.Plan, conn net.Conn, roots *x509.CertPool) (result Result, err error) {
	if conn == nil {
		return result, ErrUnavailable
	}
	defer conn.Close()
	start := time.Now()
	d := plan.Disclosure()
	if _, ok := ctx.Deadline(); !ok || !plan.Current(start) {
		return result, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	request, err := plan.RequestBytes()
	if err != nil {
		return result, ErrUnavailable
	}
	deadline := minTime(start.Add(d.Budget.TotalTimeout), d.ExpiresAt)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	deadline, _ = ctx.Deadline()
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	wire := &boundedConn{Conn: conn, readLeft: d.Budget.MaxTransportReadBytes, writeLeft: d.Budget.MaxTransportWriteBytes, readCallsLeft: d.Budget.MaxTransportReadCalls, writeCallsLeft: d.Budget.MaxTransportWriteCalls}
	defer func() {
		result.TransportReadBytes = wire.readBytes
		result.TransportWriteBytes = wire.writeBytes
		result.TransportReadCalls = wire.readCalls
		result.TransportWriteCalls = wire.writeCalls
	}()
	result.StartedAt = time.Now().Round(0).UTC()
	defer func() { result.CompletedAt = time.Now().Round(0).UTC() }()
	result.Stage = nq.HTTPSTLS
	result.Exchange = nq.HTTPSIncomplete
	result.Request = nq.HTTPSRequestNotSent
	fail := func(cause error, fallback error) (Result, error) {
		if ctx.Err() != nil {
			cause = ctx.Err()
		}
		if errors.Is(cause, context.Canceled) {
			result.Exchange = nq.HTTPSIncomplete
			return result, context.Canceled
		}
		var ne net.Error
		if errors.Is(cause, context.DeadlineExceeded) || errors.As(cause, &ne) && ne.Timeout() {
			result.Exchange = nq.HTTPSTimeout
			return result, context.DeadlineExceeded
		}
		if errors.Is(cause, ErrBudget) {
			result.Exchange = nq.HTTPSIncomplete
			return result, ErrBudget
		}
		switch fallback {
		case ErrTLS:
			result.Exchange = nq.HTTPSTLSError
		case ErrProtocol:
			result.Exchange = nq.HTTPSProtocolError
		default:
			result.Exchange = nq.HTTPSTransportError
		}
		return result, fallback
	}
	if err := conn.SetDeadline(minTime(deadline, start.Add(d.Budget.TLSHandshakeTimeout))); err != nil {
		return fail(err, ErrTransport)
	}
	client := tls.Client(wire, &tls.Config{ServerName: d.Configuration.ServerName, RootCAs: roots, MinVersion: d.Policy.MinTLSVersion, MaxVersion: d.Policy.MaxTLSVersion, NextProtos: []string{d.Policy.ALPN}, Renegotiation: tls.RenegotiateNever})
	if err := client.HandshakeContext(ctx); err != nil {
		fallback := ErrTLS
		var op *net.OpError
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &op) {
			fallback = ErrTransport
		}
		return fail(err, fallback)
	}
	state := client.ConnectionState()
	if !state.HandshakeComplete || len(state.VerifiedChains) == 0 || state.NegotiatedProtocol != d.Policy.ALPN || state.DidResume {
		return fail(ErrTLS, ErrTLS)
	}
	if err := ctx.Err(); err != nil {
		return fail(err, ErrTransport)
	}
	result.Stage = nq.HTTPSRequest
	if err := conn.SetDeadline(minTime(deadline, time.Now().Add(d.Budget.ResponseHeaderTimeout))); err != nil {
		return fail(err, ErrTransport)
	}
	// Write exactly once. A partial/error write cannot establish server receipt.
	result.Request = nq.HTTPSRequestUncertain
	n, err := client.Write(request)
	if n == len(request) {
		result.Request = nq.HTTPSRequestAccepted
	}
	if err != nil {
		return fail(err, ErrTransport)
	}
	if n != len(request) {
		return fail(io.ErrShortWrite, ErrTransport)
	}
	reader := bufio.NewReaderSize(client, 4096)
	for {
		header, err := readHeader(reader, d.Budget.MaxResponseHeaderBytes-result.ResponseHeaderBytes)
		result.ResponseHeaderBytes += len(header)
		if err != nil {
			fallback := ErrTransport
			if errors.Is(err, ErrProtocol) {
				fallback = ErrProtocol
			}
			return fail(err, fallback)
		}
		receivedAt := time.Now().Round(0).UTC()
		response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(header)), &http.Request{Method: d.Configuration.Selection.Method})
		if err != nil {
			return fail(err, ErrProtocol)
		}
		// Parse only the collected headers. Never read or close a network-backed body.
		if response.ProtoMajor != 1 || response.ProtoMinor != 1 || response.StatusCode < 100 || response.StatusCode > 599 || response.StatusCode == 101 {
			return fail(ErrProtocol, ErrProtocol)
		}
		if response.StatusCode < 200 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return fail(err, ErrTransport)
		}
		if !time.Now().Before(deadline) {
			return fail(context.DeadlineExceeded, ErrTransport)
		}
		result.ResponseReceivedAt = receivedAt
		result.StatusCode = response.StatusCode
		result.Exchange = nq.HTTPSResponseReceived
		return result, nil
	}
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Count all header bytes, including informational responses, before parsing.
// ReadByte may use buffered TLS plaintext; transport ceilings include read-ahead.
func readHeader(r *bufio.Reader, remaining int) ([]byte, error) {
	header := make([]byte, 0, min(remaining, 4096))
	for len(header) < remaining {
		ch, err := r.ReadByte()
		if err != nil {
			return header, err
		}
		header = append(header, ch)
		n := len(header)
		if ch == '\n' && (n < 2 || header[n-2] != '\r') {
			return header, ErrProtocol
		}
		if n >= 4 && bytes.Equal(header[n-4:], []byte("\r\n\r\n")) {
			return header, nil
		}
	}
	return header, ErrBudget
}
