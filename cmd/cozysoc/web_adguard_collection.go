package main

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/adguard"
	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
)

const webAdGuardReviewLifetime = 5 * time.Minute

type webAdGuardReviewState struct {
	id        string
	expiresAt time.Time
	params    api.AdGuardCollectParams
}

type webAdGuardCollectionReview struct {
	ReviewID         string               `json:"review_id"`
	ExpiresAt        time.Time            `json:"expires_at"`
	Endpoint         string               `json:"endpoint"`
	ScopeID          string               `json:"scope_id"`
	Interface        api.NetworkInterface `json:"interface"`
	MaxQueries       int                  `json:"max_queries"`
	MaxQueryAgeHours int                  `json:"max_query_age_hours"`
}

type webAdGuardCollectionResult struct {
	Outcome string                 `json:"outcome"`
	Result  *api.AdGuardCollection `json:"result,omitempty"`
}

func (h *webHandler) handleAdGuardCollectionReview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	var input struct{}
	if !decodeWebCheckBody(w, r, &input, "AdGuard Home collection") {
		return
	}
	if h.loadAdGuardStatus == nil || h.loadNetworks == nil || h.collectAdGuard == nil {
		writeWebError(w, http.StatusServiceUnavailable, "collection_unavailable", "AdGuard Home collection is unavailable")
		return
	}
	h.adguardReviewMu.Lock()
	busy := h.adguardCollecting || (h.adguardReview != nil && time.Now().Before(h.adguardReview.expiresAt))
	h.adguardReviewMu.Unlock()
	if busy {
		writeWebError(w, http.StatusConflict, "review_pending", "finish or decline the current AdGuard Home collection review")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*webRequestTimeout)
	defer cancel()
	status, err := h.loadAdGuardStatus(ctx)
	if err != nil || !status.Connected {
		writeWebError(w, http.StatusPreconditionFailed, "connection_unavailable", "connect a running AdGuard Home instance before reviewing collection")
		return
	}
	projected, ok := projectWebAdGuardStatus(status)
	if !ok || !projected.Running || !projected.QueryLogEnabled {
		writeWebError(w, http.StatusPreconditionFailed, "query_log_unavailable", "AdGuard Home query logging must be enabled before reviewing collection")
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
		writeWebError(w, http.StatusServiceUnavailable, "collection_unavailable", "AdGuard Home collection review is unavailable")
		return
	}
	expiresAt := time.Now().UTC().Add(webAdGuardReviewLifetime)
	params := api.AdGuardCollectParams{ScopeID: enrolled.ScopeID, Expected: &api.AdGuardCollectExpected{Endpoint: status.Endpoint,
		Interface: api.NetworkInterface{InterfaceName: binding.InterfaceName, InterfaceIndex: binding.InterfaceIndex, Prefixes: append([]string(nil), binding.Prefixes...)}}}
	h.adguardReviewMu.Lock()
	if h.adguardCollecting || (h.adguardReview != nil && time.Now().Before(h.adguardReview.expiresAt)) {
		h.adguardReviewMu.Unlock()
		writeWebError(w, http.StatusConflict, "review_pending", "finish or decline the current AdGuard Home collection review")
		return
	}
	h.adguardReview = &webAdGuardReviewState{id: id, expiresAt: expiresAt, params: params}
	h.adguardReviewMu.Unlock()
	writeWebJSON(w, http.StatusOK, webAdGuardCollectionReview{ReviewID: id, ExpiresAt: expiresAt, Endpoint: projected.Endpoint,
		ScopeID: enrolled.ScopeID, Interface: params.Expected.Interface, MaxQueries: adguard.MaxQueryLogEntries, MaxQueryAgeHours: int(adguard.QueryAgeWindow.Hours())})
}

func (h *webHandler) handleAdGuardCollectionRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	var input struct {
		ReviewID string `json:"review_id"`
		Approve  *bool  `json:"approve"`
	}
	if !decodeWebCheckBody(w, r, &input, "AdGuard Home collection") {
		return
	}
	if !webGatewayReviewIDPattern.MatchString(input.ReviewID) || input.Approve == nil {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "AdGuard Home collection decision is invalid")
		return
	}
	h.adguardReviewMu.Lock()
	pending := h.adguardReview
	if pending == nil || len(pending.id) != len(input.ReviewID) || subtle.ConstantTimeCompare([]byte(pending.id), []byte(input.ReviewID)) != 1 || !time.Now().Before(pending.expiresAt) {
		h.adguardReviewMu.Unlock()
		writeWebError(w, http.StatusConflict, "review_expired", "the AdGuard Home collection review is unavailable or expired")
		return
	}
	h.adguardReview = nil // Approval is one-use even if the controller call fails.
	if *input.Approve {
		h.adguardCollecting = true
	}
	h.adguardReviewMu.Unlock()
	if !*input.Approve {
		writeWebJSON(w, http.StatusOK, webAdGuardCollectionResult{Outcome: "declined"})
		return
	}
	defer func() {
		h.adguardReviewMu.Lock()
		h.adguardCollecting = false
		h.adguardReviewMu.Unlock()
	}()
	if h.collectAdGuard == nil {
		writeWebError(w, http.StatusServiceUnavailable, "collection_unavailable", "AdGuard Home collection is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	result, err := h.collectAdGuard(ctx, pending.params)
	if err != nil || ctx.Err() != nil {
		writeWebError(w, http.StatusServiceUnavailable, "collection_unavailable", "the read or storage outcome may be partial; check local history before choosing another one-shot collection")
		return
	}
	if result.ScopeID != pending.params.ScopeID || result.Read < 0 || result.Read > adguard.MaxQueryLogEntries || result.Inserted < 0 || result.Deduplicated < 0 ||
		result.SkippedOutsideScope < 0 || result.SkippedOutsideWindow < 0 || result.SkippedWithoutClientIP < 0 ||
		result.LimitReached != (result.Read == adguard.MaxQueryLogEntries) ||
		result.Inserted+result.Deduplicated+result.SkippedOutsideScope+result.SkippedOutsideWindow+result.SkippedWithoutClientIP != result.Read {
		writeWebError(w, http.StatusServiceUnavailable, "collection_unavailable", "the collection result could not be verified; check local history before retrying")
		return
	}
	writeWebJSON(w, http.StatusOK, webAdGuardCollectionResult{Outcome: "completed", Result: &result})
}
