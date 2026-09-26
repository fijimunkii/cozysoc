package main

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/opnsense"
)

const webOPNsenseReviewLifetime = 5 * time.Minute

type webOPNsenseReviewState struct {
	id        string
	expiresAt time.Time
	params    api.OPNsenseCollectParams
}

type webOPNsenseCollectionReview struct {
	ReviewID         string               `json:"review_id"`
	ExpiresAt        time.Time            `json:"expires_at"`
	Endpoint         string               `json:"endpoint"`
	ScopeID          string               `json:"scope_id"`
	Interface        api.NetworkInterface `json:"interface"`
	MaxRowsPerFamily int                  `json:"max_rows_per_family"`
}

type webOPNsenseCollectionResult struct {
	Outcome string                  `json:"outcome"`
	Result  *api.OPNsenseCollection `json:"result,omitempty"`
}

func (h *webHandler) handleOPNsenseCollectionReview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	var input struct{}
	if !decodeWebCheckBody(w, r, &input, "OPNsense collection") {
		return
	}
	if h.loadOPNsenseStatus == nil || h.loadNetworks == nil || h.collectOPNsense == nil {
		writeWebError(w, http.StatusServiceUnavailable, "collection_unavailable", "OPNsense collection is unavailable")
		return
	}
	h.opnsenseReviewMu.Lock()
	busy := h.opnsenseCollecting || (h.opnsenseReview != nil && time.Now().Before(h.opnsenseReview.expiresAt))
	h.opnsenseReviewMu.Unlock()
	if busy {
		writeWebError(w, http.StatusConflict, "review_pending", "finish or decline the current OPNsense collection review")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*webRequestTimeout)
	defer cancel()
	status, err := h.loadOPNsenseStatus(ctx)
	if err != nil || !status.Connected {
		writeWebError(w, http.StatusPreconditionFailed, "connection_unavailable", "connect an OPNsense router before reviewing collection")
		return
	}
	projected, ok := projectWebOPNsenseStatus(status)
	if !ok {
		writeWebError(w, http.StatusPreconditionFailed, "connection_unavailable", "the OPNsense connection is unavailable")
		return
	}
	networks, err := h.loadNetworks(ctx)
	if err != nil || networks.Enrolled == nil {
		writeWebError(w, http.StatusPreconditionFailed, "scope_unavailable", "enroll a home network before reviewing collection")
		return
	}
	enrolled := networks.Enrolled
	binding := devicewatch.ScopeBinding{InterfaceName: enrolled.Interface.InterfaceName, InterfaceIndex: enrolled.Interface.InterfaceIndex, Prefixes: enrolled.Interface.Prefixes}
	if enrolled.ScopeID == "" || devicewatch.ValidateScopeBinding(binding) != nil {
		writeWebError(w, http.StatusPreconditionFailed, "scope_unavailable", "the enrolled network binding is invalid")
		return
	}
	id, err := newWebToken()
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "collection_unavailable", "OPNsense collection review is unavailable")
		return
	}
	expiresAt := time.Now().UTC().Add(webOPNsenseReviewLifetime)
	params := api.OPNsenseCollectParams{ScopeID: enrolled.ScopeID, Expected: api.OPNsenseCollectExpected{Endpoint: projected.Endpoint,
		Interface: api.NetworkInterface{InterfaceName: binding.InterfaceName, InterfaceIndex: binding.InterfaceIndex, Prefixes: append([]string(nil), binding.Prefixes...)}}}
	h.opnsenseReviewMu.Lock()
	if h.opnsenseCollecting || (h.opnsenseReview != nil && time.Now().Before(h.opnsenseReview.expiresAt)) {
		h.opnsenseReviewMu.Unlock()
		writeWebError(w, http.StatusConflict, "review_pending", "finish or decline the current OPNsense collection review")
		return
	}
	h.opnsenseReview = &webOPNsenseReviewState{id: id, expiresAt: expiresAt, params: params}
	h.opnsenseReviewMu.Unlock()
	writeWebJSON(w, http.StatusOK, webOPNsenseCollectionReview{ReviewID: id, ExpiresAt: expiresAt, Endpoint: projected.Endpoint,
		ScopeID: enrolled.ScopeID, Interface: params.Expected.Interface, MaxRowsPerFamily: opnsense.MaxNeighbors})
}

func (h *webHandler) handleOPNsenseCollectionRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	var input struct {
		ReviewID string `json:"review_id"`
		Approve  *bool  `json:"approve"`
	}
	if !decodeWebCheckBody(w, r, &input, "OPNsense collection") {
		return
	}
	if !webGatewayReviewIDPattern.MatchString(input.ReviewID) || input.Approve == nil {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "OPNsense collection decision is invalid")
		return
	}
	h.opnsenseReviewMu.Lock()
	pending := h.opnsenseReview
	if pending == nil || len(pending.id) != len(input.ReviewID) || subtle.ConstantTimeCompare([]byte(pending.id), []byte(input.ReviewID)) != 1 || !time.Now().Before(pending.expiresAt) {
		h.opnsenseReviewMu.Unlock()
		writeWebError(w, http.StatusConflict, "review_expired", "the OPNsense collection review is unavailable or expired")
		return
	}
	h.opnsenseReview = nil
	if *input.Approve {
		h.opnsenseCollecting = true
	}
	h.opnsenseReviewMu.Unlock()
	if !*input.Approve {
		writeWebJSON(w, http.StatusOK, webOPNsenseCollectionResult{Outcome: "declined"})
		return
	}
	defer func() {
		h.opnsenseReviewMu.Lock()
		h.opnsenseCollecting = false
		h.opnsenseReviewMu.Unlock()
	}()
	if h.collectOPNsense == nil {
		writeWebError(w, http.StatusServiceUnavailable, "collection_unavailable", "OPNsense collection is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	result, err := h.collectOPNsense(ctx, pending.params)
	if err != nil || ctx.Err() != nil {
		writeWebError(w, http.StatusServiceUnavailable, "collection_unavailable", "the read or storage outcome may be partial; check local history before choosing another one-shot collection")
		return
	}
	if !validWebOPNsenseCollection(result, pending.params.ScopeID) {
		writeWebError(w, http.StatusServiceUnavailable, "collection_unavailable", "the collection result could not be verified; check local history before retrying")
		return
	}
	writeWebJSON(w, http.StatusOK, webOPNsenseCollectionResult{Outcome: "completed", Result: &result})
}

func validWebOPNsenseCollection(result api.OPNsenseCollection, scopeID string) bool {
	if result.ScopeID != scopeID || result.IPv4Total < 0 || result.IPv6Total < 0 || result.IPv4Total > 1000000 || result.IPv6Total > 1000000 ||
		result.IPv4Truncated != (result.IPv4Total > opnsense.MaxNeighbors) || result.IPv6Truncated != (result.IPv6Total > opnsense.MaxNeighbors) ||
		result.Read != min(result.IPv4Total, opnsense.MaxNeighbors)+min(result.IPv6Total, opnsense.MaxNeighbors) ||
		result.Inserted < 0 || result.Deduplicated < 0 || result.SkippedOutside < 0 || result.SkippedDuplicate < 0 {
		return false
	}
	return result.Inserted+result.Deduplicated+result.SkippedOutside+result.SkippedDuplicate == result.Read
}
