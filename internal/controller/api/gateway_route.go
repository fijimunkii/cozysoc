package api

import "time"

// GatewayRouteReview is metadata-only. Even a consistent route-associated
// address is not proof that a future socket is bound to it or may send traffic.
type GatewayRouteReview struct {
	State               string     `json:"state"`
	Reason              string     `json:"reason,omitempty"`
	Source              string     `json:"source,omitempty"`
	SourceAddress       string     `json:"source_address,omitempty"`
	ObservedAt          *time.Time `json:"observed_at,omitempty"`
	FreshUntil          *time.Time `json:"fresh_until,omitempty"`
	SendBindingVerified bool       `json:"send_binding_verified"`
}
