package gatewayrun

import (
	"context"

	"github.com/fijimunkii/cozysoc/internal/controller/gatewayicmp"
)

// NewICMPExecutor adapts the concrete candidate without opening a socket or
// installing it in the controller. Only Control.Run may call it after audited
// one-shot admission. A nil sender stays unavailable; there is no fallback.
func NewICMPExecutor(sender *gatewayicmp.Sender) Executor {
	if sender == nil {
		return nil
	}
	return icmpExecutor{sender: sender}
}

type icmpMeasurer interface {
	Measure(context.Context, gatewayicmp.Request) (gatewayicmp.Sample, error)
}
type icmpExecutor struct{ sender icmpMeasurer }

func (e icmpExecutor) ExecuteGateway(ctx context.Context, s Selection) (gatewayicmp.Sample, error) {
	// Preserve the exact original context/deadline, and do not share the review's
	// prefix slice. No reconstruction from an untrusted DTO or derived authority.
	s = copySelection(s)
	return e.sender.Measure(ctx, gatewayicmp.Request{Plan: s.Plan, Source: s.Source})
}
