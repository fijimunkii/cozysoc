package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

const (
	defaultWebListen       = "127.0.0.1:0"
	defaultWebUIDir        = "ui/dist"
	maxWebHeaderBytes      = 16 * 1024
	maxWebRequestURI       = 2048
	maxWebSessionBodyBytes = 1024
	webRequestTimeout      = 5 * time.Second
	webTokenBytes          = 32
	webSessionCookie       = "cozysoc_session"
)

type coverageEnvelope struct {
	AsOf    time.Time            `json:"as_of"`
	Reports []api.CoverageReport `json:"reports"`
}

type coverageLoader func(context.Context) (coverageEnvelope, error)
type deviceLoader func(context.Context) (api.DeviceList, error)

type webHandler struct {
	expectedHost       string
	uiDir              string
	loadCoverage       coverageLoader
	loadDevices        deviceLoader
	loadNetworks       networkLoader
	enrollNetwork      networkEnrollMutator
	enableDeviceWatch  deviceWatchMutator
	disableDeviceWatch deviceWatchMutator
	csrfToken          string
	bootstrapToken     string
	sessionToken       string
	bootstrapMu        sync.Mutex
	bootstrapUsed      bool
}

func runCoverageCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("coverage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("coverage takes flags only")
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	envelope, err := loadCoverageFromController(ctx, dir)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(envelope)
}

func runWeb(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	listenAddr := fs.String("listen", defaultWebListen, "loopback listen address")
	uiDir := fs.String("ui-dir", defaultWebUIDir, "built UI directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("web takes flags only")
	}
	if err := validateLoopbackListen(*listenAddr); err != nil {
		return err
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	assets, err := filepath.Abs(*uiDir)
	if err != nil {
		return fmt.Errorf("resolve UI directory: %w", err)
	}
	assets, err = filepath.EvalSymlinks(assets)
	if err != nil {
		return fmt.Errorf("resolve built UI directory symlinks: %w", err)
	}
	if err := validateUIDir(assets); err != nil {
		return err
	}
	bootstrapToken, err := newWebToken()
	if err != nil {
		return err
	}
	sessionToken, err := newWebToken()
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		return fmt.Errorf("listen for local web UI: %w", err)
	}
	defer listener.Close()
	expectedHost := listener.Addr().String()
	handler := newWebHandler(expectedHost, assets, bootstrapToken, sessionToken, func(requestCtx context.Context) (coverageEnvelope, error) {
		return loadCoverageFromController(requestCtx, dir)
	})
	handler.loadDevices = func(requestCtx context.Context) (api.DeviceList, error) {
		return loadDevicesFromController(requestCtx, dir)
	}
	if err := configureWebMutationBridge(handler, dir); err != nil {
		return err
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    maxWebHeaderBytes,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	_, _ = fmt.Fprintf(stdout, "cozysoc web ready: http://%s/#bootstrap=%s\n", expectedHost, bootstrapToken)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve local web UI: %w", err)
	}
	return nil
}

func newWebToken() (string, error) {
	raw := make([]byte, webTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate local web token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func validateLoopbackListen(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("web listen address must be IP:PORT: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("web listen address must use a literal loopback IP")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 0 || portNumber > 65535 {
		return fmt.Errorf("web listen port is invalid")
	}
	return nil
}

func validateUIDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("inspect built UI directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("built UI path is not a directory")
	}
	index, err := os.Stat(filepath.Join(dir, "index.html"))
	if err != nil {
		return fmt.Errorf("built UI is missing index.html: %w", err)
	}
	if !index.Mode().IsRegular() {
		return fmt.Errorf("built UI index.html is not a regular file")
	}
	return nil
}

func loadCoverageFromController(ctx context.Context, stateDir string) (coverageEnvelope, error) {
	requestCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	result, err := localapi.NewClient(stateDir).Call(requestCtx, api.MethodDeviceWatchCoverage)
	if err != nil {
		return coverageEnvelope{}, fmt.Errorf("load controller coverage: %w", err)
	}
	var detail api.DeviceWatchCoverage
	if err := json.Unmarshal(result, &detail); err != nil {
		return coverageEnvelope{}, fmt.Errorf("decode controller coverage: %w", err)
	}
	if detail.Coverage == nil {
		return coverageEnvelope{}, fmt.Errorf("controller coverage response is missing shared coverage")
	}
	return coverageEnvelope{
		AsOf:    detail.AsOf.UTC(),
		Reports: []api.CoverageReport{*detail.Coverage},
	}, nil
}

func loadDevicesFromController(ctx context.Context, stateDir string) (api.DeviceList, error) {
	requestCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	result, err := localapi.NewClient(stateDir).Call(requestCtx, api.MethodDevicesList)
	if err != nil {
		return api.DeviceList{}, fmt.Errorf("load controller devices: %w", err)
	}
	var devices api.DeviceList
	if err := json.Unmarshal(result, &devices); err != nil {
		return api.DeviceList{}, fmt.Errorf("decode controller devices: %w", err)
	}
	return devices, nil
}

func newWebHandler(expectedHost, uiDir, bootstrapToken, sessionToken string, load coverageLoader) *webHandler {
	return &webHandler{
		expectedHost:   expectedHost,
		uiDir:          uiDir,
		loadCoverage:   load,
		bootstrapToken: bootstrapToken,
		sessionToken:   sessionToken,
	}
}

func (h *webHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setWebSecurityHeaders(w)
	if len(r.RequestURI) > maxWebRequestURI {
		http.Error(w, "request URI is too long", http.StatusRequestURITooLong)
		return
	}
	if !h.requestAuthorityAllowed(r) {
		http.Error(w, "request authority is not allowed", http.StatusForbidden)
		return
	}

	switch r.URL.Path {
	case "/api/session":
		h.handleSessionRoute(w, r)
	case "/api/coverage":
		h.handleCoverage(w, r)
	case "/api/devices":
		h.handleDevices(w, r)
	case "/api/networks":
		h.handleNetworks(w, r)
	case "/api/networks/enroll":
		h.handleNetworkEnroll(w, r)
	case "/api/device-watch/enable":
		h.handleDeviceWatchEnable(w, r)
	case "/api/device-watch/disable":
		h.handleDeviceWatchDisable(w, r)
	default:
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		h.serveStatic(w, r)
	}
}

func (h *webHandler) requestAuthorityAllowed(r *http.Request) bool {
	if r.Host != h.expectedHost {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || parsed.Host != h.expectedHost || parsed.User != nil {
		return false
	}
	return parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

func (h *webHandler) handleSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeWebError(w, http.StatusMethodNotAllowed, "method_not_allowed", "web session bootstrap requires POST")
		return
	}
	if r.Header.Get("Origin") != "http://"+h.expectedHost {
		writeWebError(w, http.StatusForbidden, "origin_required", "web session bootstrap requires the exact local origin")
		return
	}
	if r.URL.RawQuery != "" {
		writeWebError(w, http.StatusBadRequest, "query_not_allowed", "web session bootstrap does not accept query parameters")
		return
	}
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if !strings.HasPrefix(contentType, "application/json") {
		writeWebError(w, http.StatusUnsupportedMediaType, "content_type_required", "web session bootstrap requires JSON")
		return
	}
	limited := http.MaxBytesReader(w, r.Body, maxWebSessionBodyBytes)
	defer limited.Close()
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var request struct {
		Bootstrap string `json:"bootstrap"`
	}
	if err := decoder.Decode(&request); err != nil {
		writeWebError(w, http.StatusBadRequest, "invalid_bootstrap", "web session bootstrap is invalid")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeWebError(w, http.StatusBadRequest, "invalid_bootstrap", "web session bootstrap is invalid")
		return
	}
	if !h.consumeBootstrap(request.Bootstrap) {
		writeWebError(w, http.StatusUnauthorized, "invalid_bootstrap", "web session bootstrap is invalid or already used")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     webSessionCookie,
		Value:    h.sessionToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func (h *webHandler) consumeBootstrap(candidate string) bool {
	h.bootstrapMu.Lock()
	defer h.bootstrapMu.Unlock()
	if h.bootstrapUsed || len(candidate) != len(h.bootstrapToken) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(candidate), []byte(h.bootstrapToken)) != 1 {
		return false
	}
	h.bootstrapUsed = true
	h.bootstrapToken = ""
	return true
}

func (h *webHandler) authenticated(r *http.Request) bool {
	cookie, err := r.Cookie(webSessionCookie)
	if err != nil || len(cookie.Value) != len(h.sessionToken) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(h.sessionToken)) == 1
}

func (h *webHandler) handleCoverage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeWebError(w, http.StatusMethodNotAllowed, "method_not_allowed", "coverage is read-only")
		return
	}
	if r.URL.RawQuery != "" {
		writeWebError(w, http.StatusBadRequest, "query_not_allowed", "coverage requests do not accept query parameters")
		return
	}
	if r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		writeWebError(w, http.StatusBadRequest, "request_body_not_allowed", "coverage requests do not accept a body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	envelope, err := h.loadCoverage(ctx)
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "live coverage is unavailable")
		return
	}
	writeWebJSON(w, http.StatusOK, envelope)
}

func (h *webHandler) handleDevices(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeWebError(w, http.StatusMethodNotAllowed, "method_not_allowed", "devices are read-only")
		return
	}
	if r.URL.RawQuery != "" {
		writeWebError(w, http.StatusBadRequest, "query_not_allowed", "device requests do not accept query parameters")
		return
	}
	if r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		writeWebError(w, http.StatusBadRequest, "request_body_not_allowed", "device requests do not accept a body")
		return
	}
	if h.loadDevices == nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "live devices are unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	devices, err := h.loadDevices(ctx)
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "live devices are unavailable")
		return
	}
	writeWebJSON(w, http.StatusOK, devices)
}

func (h *webHandler) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clean := path.Clean("/" + r.URL.Path)
	relative := strings.TrimPrefix(clean, "/")
	if relative == "" || relative == "." {
		relative = "index.html"
	}
	filename := filepath.Join(h.uiDir, filepath.FromSlash(relative))
	resolved, err := filepath.EvalSymlinks(filename)
	if err != nil || !pathWithinRoot(h.uiDir, resolved) {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(resolved)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func pathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func setWebSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func writeWebError(w http.ResponseWriter, status int, code, message string) {
	writeWebJSON(w, status, struct {
		Error string `json:"error"`
		Info  string `json:"message"`
	}{Error: code, Info: message})
}

func writeWebJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
