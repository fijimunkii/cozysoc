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

var (
	ErrAlreadyRunning         = errors.New("controller is already running")
	ErrInvalidMutation        = errors.New("invalid mutation request")
	ErrMutationTargetNotFound = errors.New("mutation target is not available")
	ErrMutationPrecondition   = errors.New("mutation precondition is not satisfied")
	ErrMutationConflict       = errors.New("mutation conflicts with current state")
)

type Handler interface {
	Status() api.Status
	Health() api.Health
	Capabilities() api.CapabilityList
}

type DeviceHandler interface {
	Devices(context.Context) (api.DeviceList, error)
}

type DeviceLabelHandler interface {
	LabelDevice(context.Context, api.DeviceLabelParams) (api.DeviceLabelResult, error)
}

type NetworkHandler interface {
	Networks(context.Context) (api.NetworkList, error)
}

type NetworkEnrollHandler interface {
	EnrollNetwork(context.Context, api.NetworkEnrollParams) (api.NetworkEnrollResult, error)
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
	if err := decodeStrictJSON(payload, &request); err != nil {
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
		if s.rejectUnexpectedParams(conn, request) {
			return
		}
		result = s.handler.Status()
	case api.MethodHealth:
		if s.rejectUnexpectedParams(conn, request) {
			return
		}
		result = s.handler.Health()
	case api.MethodCapabilitiesList:
		if s.rejectUnexpectedParams(conn, request) {
			return
		}
		result = s.handler.Capabilities()
	case api.MethodDevicesList:
		if s.rejectUnexpectedParams(conn, request) {
			return
		}
		deviceHandler, ok := s.handler.(DeviceHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		requestCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		deviceList, deviceErr := deviceHandler.Devices(requestCtx)
		cancel()
		if deviceErr != nil {
			s.logger.Warn("local_api_request_failed", "method", api.MethodDevicesList)
			s.writeError(conn, request.ID, "internal_error", "unable to load devices")
			return
		}
		result = deviceList
	case api.MethodDeviceLabel:
		labelHandler, ok := s.handler.(DeviceLabelHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		var params api.DeviceLabelParams
		if err := decodeRequiredParams(request.Params, &params); err != nil || params.DeviceID == "" || params.Label == nil {
			s.writeError(conn, request.ID, "invalid_request", "invalid device label parameters")
			return
		}
		requestCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		labelResult, labelErr := labelHandler.LabelDevice(requestCtx, params)
		cancel()
		if labelErr != nil {
			switch {
			case errors.Is(labelErr, ErrInvalidMutation):
				s.writeError(conn, request.ID, "invalid_request", "invalid device label parameters")
			case errors.Is(labelErr, ErrMutationTargetNotFound):
				s.writeError(conn, request.ID, "not_found", "device is not available")
			default:
				s.logger.Warn("local_api_request_failed", "method", api.MethodDeviceLabel)
				s.writeError(conn, request.ID, "internal_error", "unable to update device label")
			}
			return
		}
		result = labelResult
	case api.MethodNetworksList:
		if s.rejectUnexpectedParams(conn, request) {
			return
		}
		networkHandler, ok := s.handler.(NetworkHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		requestCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		networkList, networkErr := networkHandler.Networks(requestCtx)
		cancel()
		if networkErr != nil {
			s.logger.Warn("local_api_request_failed", "method", api.MethodNetworksList)
			s.writeError(conn, request.ID, "internal_error", "unable to load network enrollment state")
			return
		}
		result = networkList
	case api.MethodNetworkEnroll:
		enrollHandler, ok := s.handler.(NetworkEnrollHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		var params api.NetworkEnrollParams
		if err := decodeRequiredParams(request.Params, &params); err != nil || params.InterfaceName == "" {
			s.writeError(conn, request.ID, "invalid_request", "invalid network enrollment parameters")
			return
		}
		requestCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		enrollResult, enrollErr := enrollHandler.EnrollNetwork(requestCtx, params)
		cancel()
		if enrollErr != nil {
			switch {
			case errors.Is(enrollErr, ErrInvalidMutation):
				s.writeError(conn, request.ID, "invalid_request", "invalid network enrollment parameters")
			case errors.Is(enrollErr, ErrMutationPrecondition):
				s.writeError(conn, request.ID, "precondition_failed", "interface is not eligible for enrollment")
			case errors.Is(enrollErr, ErrMutationConflict):
				s.writeError(conn, request.ID, "conflict", "a different network is already enrolled")
			default:
				s.logger.Warn("local_api_request_failed", "method", api.MethodNetworkEnroll)
				s.writeError(conn, request.ID, "internal_error", "unable to enroll network")
			}
			return
		}
		result = enrollResult
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

func (s *Server) rejectUnexpectedParams(conn net.Conn, request api.Request) bool {
	if !hasRequestParams(request.Params) {
		return false
	}
	s.writeError(conn, request.ID, "invalid_request", "method does not accept parameters")
	return true
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
