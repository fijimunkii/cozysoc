package resolverrun

import "crypto/subtle"

// Discard revokes only the matching pending review, without granting consent,
// changing cooldown, canceling an admitted run, or creating an audit. A transport
// must call this when its review connection is declined, lost, or abandoned.
// It remains safe after shutdown and cannot discard a newer unrelated review.
func (c *Control) Discard(ticket Ticket) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending != nil && subtle.ConstantTimeCompare(ticket.key[:], c.pending.ticket.key[:]) == 1 {
		c.pending = nil
	}
}
