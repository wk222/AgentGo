// 面板通用小件：标题栏、徽标、小按钮、行容器、空状态。迁移自旧 panels/index.tsx（逻辑与样式不变）。
import type { VNode } from 'vue'

export const panelBodyStyle = {
  display: 'flex', flexDirection: 'column' as const,
  height: '100%', overflow: 'hidden',
}
export const scrollStyle = { flex: 1, overflowY: 'auto' as const, padding: '16px 20px' }

export function EmptyState(msg: string) {
  return (
    <div style={{ textAlign: 'center', color: 'var(--text-dim)', padding: '40px 0', fontSize: '13px' }}>
      {msg}
    </div>
  )
}

export const PANEL_HEADER_SVGS: Record<string, string> = {
  '⚡': '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg>',
  '📋': '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="8" y="2" width="8" height="4" rx="1"/><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/><path d="M9 12h6"/><path d="M9 16h6"/></svg>',
  '🛡': '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg>',
  '⭐': '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>',
  '🌐': '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>',
  '📦': '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/></svg>',
}

export function PanelHeader(icon: string, title: string) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: '8px',
      padding: '10px 18px', borderBottom: '1px solid var(--border)', flexShrink: 0,
      background: 'var(--surface)',
    }}>
      <span
        style={{
          display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
          width: '24px', height: '24px', borderRadius: '6px',
          background: 'var(--accent-bg)', color: 'var(--accent-text)',
        }}
        innerHTML={PANEL_HEADER_SVGS[icon] || PANEL_HEADER_SVGS['⚡']}
      ></span>
      <span style={{ fontSize: '13.5px', fontWeight: 600, color: 'var(--text-strong)', letterSpacing: '-0.01em' }}>{title}</span>
    </div>
  )
}

export function badge(label: string, color?: string) {
  return (
    <span style={{
      fontSize: '11px', padding: '2px 8px', borderRadius: '6px', flexShrink: 0,
      background: color ? `color-mix(in srgb, ${color} 14%, var(--surface))` : 'var(--accent-bg)',
      color: color || 'var(--accent-text)',
      border: `1px solid ${color ? `color-mix(in srgb, ${color} 30%, transparent)` : 'color-mix(in srgb, var(--accent) 26%, transparent)'}`,
      fontWeight: 600,
    }}>{label}</span>
  )
}

export const miniButtonStyle = (primary = false) => ({
  padding: '4px 10px',
  border: primary ? '1px solid color-mix(in srgb, var(--accent) 32%, transparent)' : '1px solid var(--border)',
  borderRadius: '6px',
  background: primary ? 'var(--accent-bg)' : 'var(--bg)',
  color: primary ? 'var(--accent-text)' : 'var(--text)',
  fontSize: '11.5px',
  cursor: 'pointer',
  fontWeight: primary ? 600 : 500,
  whiteSpace: 'nowrap' as const,
})

export function rowDiv(children: VNode | VNode[]) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: '12px',
      padding: '10px 14px', borderRadius: '9px',
      background: 'var(--surface)', border: '1px solid var(--border)',
      boxShadow: 'var(--shadow-sm)',
      marginBottom: '8px', fontSize: '13px', color: 'var(--text)',
    }}>{children}</div>
  )
}

export const statusColor = (s: string) => {
  if (!s) return 'var(--text-dim)'
  if (s === 'active' || s === 'connected' || s === 'enabled' || s === 'done' || s === 'completed') return '#5cb85c'
  if (s === 'running') return '#3b9eff'
  if (s === 'failed' || s === 'error') return '#e05c5c'
  return 'var(--text-dim)'
}
