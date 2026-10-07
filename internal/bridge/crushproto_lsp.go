package bridge

import (
	"context"

	ct "github.com/charmbracelet/crush/pkg/codetools"

	"agentgo/internal/crushproto"
)

type crushprotoLSPBridge struct {
	svc *AppService
}

func (b *crushprotoLSPBridge) LSPStates(workDir string) map[string]crushproto.LSPClientInfo {
	tb := b.svc.getCodetools()
	if tb == nil {
		return map[string]crushproto.LSPClientInfo{}
	}
	states := tb.LSPStates()
	out := make(map[string]crushproto.LSPClientInfo, len(states))
	for k, v := range states {
		out[k] = crushproto.LSPClientInfo{
			Name:            v.Name,
			State:           v.State,
			Error:           v.Error,
			DiagnosticCount: v.DiagnosticCount,
			ConnectedAt:     v.ConnectedAt,
		}
	}
	return out
}

func (b *crushprotoLSPBridge) LSPDiagnostics(workDir, lspName string) any {
	tb := b.svc.getCodetools()
	if tb == nil {
		return map[string]any{}
	}
	return tb.LSPDiagnostics(lspName)
}

func (b *crushprotoLSPBridge) StartLSP(ctx context.Context, workDir, path string) {
	tb := b.svc.getCodetools()
	if tb != nil {
		tb.StartLSP(ctx, path)
	}
}

func (b *crushprotoLSPBridge) StopAllLSPs(ctx context.Context, workDir string) {
	tb := b.svc.getCodetools()
	if tb != nil {
		tb.StopAllLSPs(ctx)
	}
}

func (b *crushprotoLSPBridge) SetLSPEventListener(workDir string, fn func(evType, name string, state int, diagCount int)) {
	b.svc.setLSPListener(func(e ct.LSPEvent) {
		fn(e.Type, e.Name, e.State, e.DiagnosticCount)
	})
}
