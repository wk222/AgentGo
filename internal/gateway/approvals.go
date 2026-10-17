package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// ApprovalResolver is an optional Backend capability: settle a pending
// approval and stream the run it unblocks. emit receives the same "chunk"
// events StreamChat does, plus an "interrupt" event when the continued run
// stops for yet another approval.
type ApprovalResolver interface {
	ResolveApproval(ctx context.Context, approvalID string, approve bool, emit func(event string, data []byte) error) (*ChatResult, error)
}

// handleApprovals serves POST /api/v1/approvals/{id} with {"approve": bool}.
// The response is an SSE stream shaped like /api/v1/chat/stream
// (chunk* [interrupt] (error | done)), so a client can chain approvals: every
// "interrupt" carries the id of the next one.
func (s *Server) handleApprovals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/approvals/"), "/")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "approval id required: POST /api/v1/approvals/{id}", http.StatusBadRequest)
		return
	}
	resolver, ok := s.backend.(ApprovalResolver)
	if !ok {
		http.Error(w, "approvals are not supported by this backend", http.StatusNotImplemented)
		return
	}
	var in struct {
		Approve bool `json:"approve"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		http.Error(w, "body must be {\"approve\": true|false}: "+err.Error(), http.StatusBadRequest)
		return
	}
	swr, err := NewSSEWriter(w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	emit := func(event string, data []byte) error {
		var payload any
		_ = json.Unmarshal(data, &payload)
		return swr.WriteEvent(event, payload)
	}
	res, err := resolver.ResolveApproval(r.Context(), id, in.Approve, emit)
	if err != nil {
		_ = swr.WriteEvent("error", map[string]string{"error": err.Error()})
		return
	}
	_ = swr.WriteEvent("done", res)
}
