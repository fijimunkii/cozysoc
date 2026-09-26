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
	ErrInvalidRead            = errors.New("invalid read request")
	ErrReadTargetNotFound     = errors.New("read target is not available")
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

type StorageOverviewHandler interface {
	StorageOverview(context.Context) (api.StorageOverview, error)
}

type DiagnosticsPreviewHandler interface {
	DiagnosticsPreview(context.Context) (api.DiagnosticPreview, error)
}

type DeviceHandler interface {
	Devices(context.Context) (api.DeviceList, error)
}

type DeviceWatchCoverageHandler interface {
	DeviceWatchCoverage(context.Context) (api.DeviceWatchCoverage, error)
}

type DeviceDetailHandler interface {
	DeviceDetail(context.Context, api.DeviceDetailParams) (api.DeviceDetail, error)
}

type DeviceActivityHandler interface {
	DeviceActivity(context.Context) (api.DeviceActivityList, error)
}

type DeviceLabelHandler interface {
	LabelDevice(context.Context, api.DeviceLabelParams) (api.DeviceLabelResult, error)
}

type DeviceIdentityHandler interface {
	MergeDevices(context.Context, api.DeviceMergeParams) (api.DeviceIdentityCorrectionResult, error)
	UnmergeDevices(context.Context, api.DeviceUnmergeParams) (api.DeviceIdentityCorrectionResult, error)
}

type DeviceMergeListHandler interface {
	DeviceMerges(context.Context) (api.DeviceMergeList, error)
}

type DeviceSplitHandler interface {
	SplitDeviceObservation(context.Context, api.DeviceSplitParams) (api.DeviceSplitResult, error)
	UnsplitDeviceObservation(context.Context, api.DeviceUnsplitParams) (api.DeviceSplitResult, error)
	DeviceSplits(context.Context) (api.DeviceSplitList, error)
}

type NetworkHandler interface {
	Networks(context.Context) (api.NetworkList, error)
}

type NetworkEnrollHandler interface {
	EnrollNetwork(context.Context, api.NetworkEnrollParams) (api.NetworkEnrollResult, error)
}

type NetworkRetireHandler interface {
	RetireNetwork(context.Context, api.NetworkRetireParams) (api.NetworkRetireResult, error)
}

type DeviceWatchControlHandler interface {
	EnableDeviceWatch(context.Context) (api.DeviceWatchControlResult, error)
	DisableDeviceWatch(context.Context) (api.DeviceWatchControlResult, error)
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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = s.listener.Close() })
	defer stop()

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
				s.handleConnContext(ctx, conn)
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
	s.handleConnContext(context.Background(), conn)
}

func (s *Server) handleConnContext(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
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
	case api.MethodAdGuardConnect, api.MethodAdGuardStatus, api.MethodAdGuardDisconnect, api.MethodAdGuardCollect:
		if !identity.Verified {
			s.writeError(conn, request.ID, "unauthorized", "verified OS identity is required")
			return
		}
		s.handleAdGuard(ctx, conn, request)
		return
	case api.MethodHTTPSCheck:
		if !identity.Verified {
			s.writeError(conn, request.ID, "unauthorized", "verified OS identity is required")
			return
		}
		s.httpsCheck(ctx, conn, reader, payload, request)
		return
	case api.MethodResolverCheck:
		if !identity.Verified {
			s.writeError(conn, request.ID, "unauthorized", "verified OS identity is required")
			return
		}
		s.resolverCheck(ctx, conn, reader, payload, request)
		return
	case api.MethodGatewayCheck:
		if !identity.Verified {
			s.writeError(conn, request.ID, "unauthorized", "verified OS identity is required")
			return
		}
		s.gatewayCheck(ctx, conn, reader, payload, request)
		return
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
	case api.MethodStorageOverview:
		if s.rejectUnexpectedParams(conn, request) {
			return
		}
		storageHandler, ok := s.handler.(StorageOverviewHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		overview, overviewErr := storageHandler.StorageOverview(requestCtx)
		cancel()
		if overviewErr != nil {
			s.logger.Warn("local_api_request_failed", "method", api.MethodStorageOverview)
			s.writeError(conn, request.ID, "internal_error", "unable to load storage overview")
			return
		}
		result = overview
	case api.MethodDiagnosticsPreview:
		if s.rejectUnexpectedParams(conn, request) {
			return
		}
		previewHandler, ok := s.handler.(DiagnosticsPreviewHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		preview, previewErr := previewHandler.DiagnosticsPreview(requestCtx)
		cancel()
		if previewErr != nil {
			s.logger.Warn("local_api_request_failed", "method", api.MethodDiagnosticsPreview)
			s.writeError(conn, request.ID, "internal_error", "unable to build diagnostic preview")
			return
		}
		result = preview
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
	case api.MethodDeviceMerges:
		if s.rejectUnexpectedParams(conn, request) {
			return
		}
		mergeHandler, ok := s.handler.(DeviceMergeListHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		merges, mergeErr := mergeHandler.DeviceMerges(requestCtx)
		cancel()
		if mergeErr != nil {
			s.logger.Warn("local_api_request_failed", "method", api.MethodDeviceMerges)
			s.writeError(conn, request.ID, "internal_error", "unable to load device corrections")
			return
		}
		result = merges
	case api.MethodDeviceSplits:
		if s.rejectUnexpectedParams(conn, request) {
			return
		}
		splitHandler, ok := s.handler.(DeviceSplitHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		splits, splitErr := splitHandler.DeviceSplits(requestCtx)
		cancel()
		if splitErr != nil {
			s.logger.Warn("local_api_request_failed", "method", api.MethodDeviceSplits)
			s.writeError(conn, request.ID, "internal_error", "unable to load device splits")
			return
		}
		result = splits
	case api.MethodDeviceActivity:
		if s.rejectUnexpectedParams(conn, request) {
			return
		}
		activityHandler, ok := s.handler.(DeviceActivityHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		requestCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		activity, activityErr := activityHandler.DeviceActivity(requestCtx)
		cancel()
		if activityErr != nil {
			s.logger.Warn("local_api_request_failed", "method", api.MethodDeviceActivity)
			s.writeError(conn, request.ID, "internal_error", "unable to load device activity")
			return
		}
		result = activity
	case api.MethodDeviceDetail:
		detailHandler, ok := s.handler.(DeviceDetailHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		var params api.DeviceDetailParams
		if err := decodeRequiredParams(request.Params, &params); err != nil || params.DeviceID == "" {
			s.writeError(conn, request.ID, "invalid_request", "invalid device detail parameters")
			return
		}
		requestCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		detail, detailErr := detailHandler.DeviceDetail(requestCtx, params)
		cancel()
		if detailErr != nil {
			switch {
			case errors.Is(detailErr, ErrInvalidRead):
				s.writeError(conn, request.ID, "invalid_request", "invalid device detail parameters")
			case errors.Is(detailErr, ErrReadTargetNotFound):
				s.writeError(conn, request.ID, "not_found", "device evidence is not available")
			default:
				s.logger.Warn("local_api_request_failed", "method", api.MethodDeviceDetail)
				s.writeError(conn, request.ID, "internal_error", "unable to load device detail")
			}
			return
		}
		result = detail
	case api.MethodDeviceWatchCoverage:
		if s.rejectUnexpectedParams(conn, request) {
			return
		}
		coverageHandler, ok := s.handler.(DeviceWatchCoverageHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		requestCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		coverage, coverageErr := coverageHandler.DeviceWatchCoverage(requestCtx)
		cancel()
		if coverageErr != nil {
			s.logger.Warn("local_api_request_failed", "method", api.MethodDeviceWatchCoverage)
			s.writeError(conn, request.ID, "internal_error", "unable to load Device Watch coverage")
			return
		}
		result = coverage
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
	case api.MethodDeviceMerge, api.MethodDeviceUnmerge:
		identityHandler, ok := s.handler.(DeviceIdentityHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		requestCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		var correction api.DeviceIdentityCorrectionResult
		var correctionErr error
		if request.Method == api.MethodDeviceMerge {
			var params api.DeviceMergeParams
			if err := decodeRequiredParams(request.Params, &params); err != nil || params.SourceDeviceID == "" || params.TargetDeviceID == "" {
				cancel()
				s.writeError(conn, request.ID, "invalid_request", "invalid device identity parameters")
				return
			}
			correction, correctionErr = identityHandler.MergeDevices(requestCtx, params)
		} else {
			var params api.DeviceUnmergeParams
			if err := decodeRequiredParams(request.Params, &params); err != nil || params.SourceDeviceID == "" {
				cancel()
				s.writeError(conn, request.ID, "invalid_request", "invalid device identity parameters")
				return
			}
			correction, correctionErr = identityHandler.UnmergeDevices(requestCtx, params)
		}
		cancel()
		if correctionErr != nil {
			switch {
			case errors.Is(correctionErr, ErrInvalidMutation):
				s.writeError(conn, request.ID, "invalid_request", "invalid device identity parameters")
			case errors.Is(correctionErr, ErrMutationTargetNotFound):
				s.writeError(conn, request.ID, "not_found", "device is not available")
			case errors.Is(correctionErr, ErrMutationConflict):
				s.writeError(conn, request.ID, "conflict", "device identity correction conflicts with current state")
			default:
				s.logger.Warn("local_api_request_failed", "method", request.Method)
				s.writeError(conn, request.ID, "internal_error", "unable to correct device identity")
			}
			return
		}
		result = correction
	case api.MethodDeviceSplit, api.MethodDeviceUnsplit:
		splitHandler, ok := s.handler.(DeviceSplitHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		requestCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		var correction api.DeviceSplitResult
		var correctionErr error
		if request.Method == api.MethodDeviceSplit {
			var params api.DeviceSplitParams
			if err := decodeRequiredParams(request.Params, &params); err != nil || params.SourceDeviceID == "" || params.ObservationID == "" {
				cancel()
				s.writeError(conn, request.ID, "invalid_request", "invalid device split parameters")
				return
			}
			correction, correctionErr = splitHandler.SplitDeviceObservation(requestCtx, params)
		} else {
			var params api.DeviceUnsplitParams
			if err := decodeRequiredParams(request.Params, &params); err != nil || params.ObservationID == "" {
				cancel()
				s.writeError(conn, request.ID, "invalid_request", "invalid device split parameters")
				return
			}
			correction, correctionErr = splitHandler.UnsplitDeviceObservation(requestCtx, params)
		}
		cancel()
		if correctionErr != nil {
			switch {
			case errors.Is(correctionErr, ErrInvalidMutation):
				s.writeError(conn, request.ID, "invalid_request", "invalid device split parameters")
			case errors.Is(correctionErr, ErrMutationTargetNotFound):
				s.writeError(conn, request.ID, "not_found", "device observation is not available")
			case errors.Is(correctionErr, ErrMutationConflict):
				s.writeError(conn, request.ID, "conflict", "device split conflicts with current state")
			default:
				s.logger.Warn("local_api_request_failed", "method", request.Method)
				s.writeError(conn, request.ID, "internal_error", "unable to split device observation")
			}
			return
		}
		result = correction
	case api.MethodQualityDiagnosis:
		if !identity.Verified {
			s.writeError(conn, request.ID, "unauthorized", "verified OS identity is required")
			return
		}
		diagnosis, ok := s.readQualityDiagnosis(ctx, conn, request)
		if !ok {
			return
		}
		result = diagnosis
	case api.MethodResolverHistory:
		if !identity.Verified {
			s.writeError(conn, request.ID, "unauthorized", "verified OS identity is required")
			return
		}
		history, ok := s.readResolverHistory(ctx, conn, request)
		if !ok {
			return
		}
		result = history
	case api.MethodHTTPSHistory:
		if !identity.Verified {
			s.writeError(conn, request.ID, "unauthorized", "verified OS identity is required")
			return
		}
		history, ok := s.readHTTPSHistory(ctx, conn, request)
		if !ok {
			return
		}
		result = history
	case api.MethodGatewayHistory:
		if !identity.Verified {
			s.writeError(conn, request.ID, "unauthorized", "verified OS identity is required")
			return
		}
		history, ok := s.readGatewayHistory(ctx, conn, request)
		if !ok {
			return
		}
		result = history
	case api.MethodHTTPSSave, api.MethodHTTPSList, api.MethodHTTPSRetire, api.MethodHTTPSPlan:
		if !identity.Verified {
			s.writeError(conn, request.ID, "unauthorized", "verified OS identity is required")
			return
		}
		value, ok := s.httpsSettings(ctx, conn, request)
		if !ok {
			return
		}
		result = value
	case api.MethodResolverSave, api.MethodResolverList, api.MethodResolverRetire, api.MethodResolverPlan:
		if !identity.Verified {
			s.writeError(conn, request.ID, "unauthorized", "verified OS identity is required")
			return
		}
		value, ok := s.resolverSettings(ctx, conn, request)
		if !ok {
			return
		}
		result = value
	case api.MethodNetworkQualityGatewayPlan:
		plan, ok := s.readGatewayPlan(conn, request)
		if !ok {
			return
		}
		result = plan
	case api.MethodNetworkQualityLocal:
		quality, ok := s.readLocalNetworkQuality(conn, request)
		if !ok {
			return
		}
		result = quality
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
	case api.MethodNetworkRetire:
		retireHandler, ok := s.handler.(NetworkRetireHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		var params api.NetworkRetireParams
		if err := decodeRequiredParams(request.Params, &params); err != nil || params.ScopeID == "" {
			s.writeError(conn, request.ID, "invalid_request", "invalid network retirement parameters")
			return
		}
		requestCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		retired, retireErr := retireHandler.RetireNetwork(requestCtx, params)
		cancel()
		if retireErr != nil {
			switch {
			case errors.Is(retireErr, ErrInvalidMutation):
				s.writeError(conn, request.ID, "invalid_request", "invalid network retirement parameters")
			case errors.Is(retireErr, ErrMutationPrecondition):
				s.writeError(conn, request.ID, "precondition_failed", "disable Device Watch before retiring the network")
			case errors.Is(retireErr, ErrMutationConflict):
				s.writeError(conn, request.ID, "conflict", "the enrolled network changed; review it again")
			default:
				s.logger.Warn("local_api_request_failed", "method", api.MethodNetworkRetire)
				s.writeError(conn, request.ID, "internal_error", "unable to retire network")
			}
			return
		}
		result = retired
	case api.MethodDeviceWatchEnable, api.MethodDeviceWatchDisable:
		if s.rejectUnexpectedParams(conn, request) {
			return
		}
		controlHandler, ok := s.handler.(DeviceWatchControlHandler)
		if !ok {
			s.writeError(conn, request.ID, "method_not_found", "method is not available")
			return
		}
		requestCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		var controlResult api.DeviceWatchControlResult
		var controlErr error
		if request.Method == api.MethodDeviceWatchEnable {
			controlResult, controlErr = controlHandler.EnableDeviceWatch(requestCtx)
		} else {
			controlResult, controlErr = controlHandler.DisableDeviceWatch(requestCtx)
		}
		cancel()
		if controlErr != nil {
			switch {
			case errors.Is(controlErr, ErrInvalidMutation):
				s.writeError(conn, request.ID, "invalid_request", "invalid Device Watch control request")
			case errors.Is(controlErr, ErrMutationPrecondition):
				s.writeError(conn, request.ID, "precondition_failed", "Device Watch prerequisites are not satisfied")
			case errors.Is(controlErr, ErrMutationConflict):
				s.writeError(conn, request.ID, "conflict", "Device Watch state conflicts with current configuration")
			default:
				s.logger.Warn("local_api_request_failed", "method", request.Method)
				s.writeError(conn, request.ID, "internal_error", "unable to update Device Watch state")
			}
			return
		}
		result = controlResult
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
