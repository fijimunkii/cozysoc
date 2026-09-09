package localapi

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

const (
	SocketFilename      = "controller.sock"
	maxRequestBytes     = 64 * 1024
	maxConcurrentClient = 32
	requestTimeout      = 5 * time.Second
)

var ErrAlreadyRunning = errors.New("controller is already running")

type Handler interface {
	Status() api.Status
	Health() api.Health
	Capabilities() api.CapabilityList
}

type Server struct {
	listener   net.Listener
	stateDir   string
	socket     string
	secret     string
	handler    Handler
	logger     *slog.Logger
	sem        chan struct{}
	verifyPeer peerVerifier
}

func NewServer(stateDir string, handler Handler, logger *slog.Logger) (*Server, error) {
	return newServer(stateDir, handler, logger, verifyPeer)
}

func newServer(stateDir string, handler Handler, logger *slog.Logger, verifier peerVerifier) (*Server, error) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if verifier == nil {
		return nil, fmt.Errorf("local API peer verifier is required")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	if err := os.Chmod(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure state directory: %w", err)
	}

	socket := filepath.Join(stateDir, SocketFilename)
	if err := prepareSocket(socket); err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("listen on local controller socket: %w", err)
	}
	cleanup := func() {
		_ = listener.Close()
		_ = os.Remove(socket)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		cleanup()
		return nil, fmt.Errorf("secure controller socket: %w", err)
	}
	secret, err := createSessionSecret(stateDir)
	if err != nil {
		cleanup()
		return nil, err
	}

	return &Server{
		listener:   listener,
		stateDir:   stateDir,
		socket:     socket,
		secret:     secret,
		handler:    handler,
		logger:     logger,
		sem:        make(chan struct{}, maxConcurrentClient),
		verifyPeer: verifier,
	}, nil
}

func prepareSocket(socket string) error {
	info, err := os.Lstat(socket)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect controller socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket path at %s", socket)
	}

	conn, err := net.DialTimeout("unix", socket, 250*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return ErrAlreadyRunning
	}
	if err := os.Remove(socket); err != nil {
		return fmt.Errorf("remove stale controller socket: %w", err)
	}
	return nil
}

func (s *Server) SocketPath() string {
	return s.socket
}

func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return ctx.Err()
			}
			return fmt.Errorf("accept local controller connection: %w", err)
		}

		select {
		case s.sem <- struct{}{}:
			go func() {
				defer func() { <-s.sem }()
				s.handleConn(conn)
			}()
		default:
			_ = conn.Close()
			s.logger.Warn("local_api_connection_rejected", "reason", "concurrency_limit")
		}
	}
}

func (s *Server) Close() error {
	err := s.listener.Close()
	if secretErr := removeSessionSecret(s.stateDir); secretErr != nil && err == nil {
		err = secretErr
	}
	removeErr := os.Remove(s.socket)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
		err = removeErr
	}
	return err
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(requestTimeout))

	identity, err := s.verifyPeer(conn)
	if err != nil {
		s.logger.Warn("local_api_connection_rejected", "reason", "peer_identity_error")
		s.writeError(conn, "", "unauthorized", "authentication failed")
		return
	}
	if identity.Verified && identity.UID != os.Geteuid() {
		s.logger.Warn("local_api_connection_rejected", "reason", "peer_uid_mismatch")
		s.writeError(conn, "", "unauthorized", "authentication failed")
		return
	}

	limited := io.LimitReader(conn, maxRequestBytes+1)
	reader := bufio.NewReader(limited)
	payload, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		s.writeError(conn, "", "invalid_request", "unable to read request")
		return
	}
	if len(payload) > maxRequestBytes {
		s.writeError(conn, "", "request_too_large", "request exceeds local API limit")
		return
	}

	var request api.Request
	if err := json.Unmarshal(payload, &request); err != nil {
		s.writeError(conn, "", "invalid_request", "request is not valid JSON")
		return
	}
	if subtle.ConstantTimeCompare([]byte(request.Auth), []byte(s.secret)) != 1 {
		s.logger.Warn("local_api_connection_rejected", "reason", "invalid_session_secret")
		s.writeError(conn, request.ID, "unauthorized", "authentication failed")
		return
	}
	if request.Version != api.Version {
		s.writeError(conn, request.ID, "version_mismatch", "unsupported API version")
		return
	}

	var result any
	switch request.Method {
	case api.MethodStatus:
		result = s.handler.Status()
	case api.MethodHealth:
		result = s.handler.Health()
	case api.MethodCapabilitiesList:
		result = s.handler.Capabilities()
	default:
		s.writeError(conn, request.ID, "method_not_found", "method is not available")
		return
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		s.writeError(conn, request.ID, "internal_error", "unable to encode response")
		return
	}
	_ = json.NewEncoder(conn).Encode(api.Response{
		Version: api.Version,
		ID:      request.ID,
		Result:  encoded,
	})
}

func (s *Server) writeError(w io.Writer, id, code, message string) {
	_ = json.NewEncoder(w).Encode(api.Response{
		Version: api.Version,
		ID:      id,
		Error: &api.Error{
			Code:    code,
			Message: message,
		},
	})
}
