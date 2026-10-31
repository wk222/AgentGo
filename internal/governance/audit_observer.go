package governance

import "context"

// AuditObserver sees every policy decision of a tool call as it is recorded (allow, deny, approval asked,
// approved, rejected, executed). callID is the model's tool-call id when the run supplied one.
type AuditObserver func(callID, action, toolName, result, risk, explanation string)

type auditObserverKey struct{}

// WithAuditObserver lets a caller (the debug call trace) follow policy decisions without reading the audit log.
func WithAuditObserver(ctx context.Context, o AuditObserver) context.Context {
	if o == nil {
		return ctx
	}
	return context.WithValue(ctx, auditObserverKey{}, o)
}

func notifyAuditObserver(ctx context.Context, action, toolName, result, risk, explanation string) {
	if o, ok := ctx.Value(auditObserverKey{}).(AuditObserver); ok && o != nil {
		o(callIDFrom(ctx), action, toolName, result, risk, explanation)
	}
}
