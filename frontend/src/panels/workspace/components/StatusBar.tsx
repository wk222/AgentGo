import { defineComponent, type PropType } from 'vue'
import type { OpenTab, CursorPositionInfo } from '../types'

export const StatusBar = defineComponent({
  name: 'StatusBar',
  props: {
    activeTab: { type: Object as PropType<OpenTab | null>, default: null },
    cursorPos: { type: Object as PropType<CursorPositionInfo>, default: () => ({ line: 1, column: 1, selectedChars: 0 }) },
    gitBranch: { type: String, default: 'main' },
    isTerminalOpen: { type: Boolean, default: false },
    diagnosticCount: { type: Number, default: 0 },
  },
  emits: ['toggleTerminal', 'openCommandPalette', 'changeEncoding', 'changeEOL', 'changeIndent', 'formatDocument', 'checkProblems'],
  setup(props, { emit }) {
    const itemStyle = {
      display: 'inline-flex',
      alignItems: 'center',
      gap: '4px',
      padding: '2px 8px',
      cursor: 'pointer',
      borderRadius: '3px',
      transition: 'background-color 0.15s ease',
      fontSize: '11px',
      fontFamily: 'var(--font-ui, sans-serif)',
      color: 'var(--text-dim, #A1A1AA)',
      userSelect: 'none' as const,
    }

    return () => (
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          height: '24px',
          padding: '0 8px',
          backgroundColor: 'var(--sidebar, #18181B)',
          borderTop: '1px solid var(--border-subtle, rgba(255,255,255,0.08))',
          fontSize: '11px',
          color: 'var(--text-dim, #A1A1AA)',
          flexShrink: 0,
          zIndex: 10,
        }}
      >
        {/* Left Area: Git Branch & Terminal status */}
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
          {/* Git Branch */}
          <div
            style={itemStyle}
            title={`Current Branch: ${props.gitBranch}`}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="6" y1="3" x2="6" y2="15" />
              <circle cx="18" cy="6" r="3" />
              <circle cx="6" cy="18" r="3" />
              <path d="M18 9a9 9 0 0 1-9 9" />
            </svg>
            <span>{props.gitBranch || 'git'}</span>
          </div>

          {/* Quick Terminal Button */}
          <div
            style={{
              ...itemStyle,
              backgroundColor: props.isTerminalOpen ? 'var(--accent-bg, rgba(56, 189, 248, 0.15))' : 'transparent',
              color: props.isTerminalOpen ? 'var(--accent, #38BDF8)' : 'inherit',
            }}
            onClick={() => emit('toggleTerminal')}
            title="Toggle Terminal Panel"
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="4 17 10 11 4 5" />
              <line x1="12" y1="19" x2="20" y2="19" />
            </svg>
            <span>Terminal</span>
          </div>

          {/* Problems Indicator */}
          <div
            style={{
              ...itemStyle,
              color: props.diagnosticCount > 0 ? '#F87171' : 'var(--text-dim, #A1A1AA)',
            }}
            onClick={() => emit('checkProblems')}
            title={props.diagnosticCount > 0 ? `${props.diagnosticCount} 个问题/诊断` : '无语法与诊断错误'}
          >
            <span>⊗ {props.diagnosticCount}</span>
            <span>⚠ 0</span>
          </div>
        </div>

        {/* Right Area: Line/Col, Encoding, EOL, Indent, Language */}
        <div style={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
          {props.activeTab && (
            <>
              {/* Line & Column */}
              <div
                style={itemStyle}
                onClick={() => emit('openCommandPalette')}
                title="Go to Line / Command Palette"
              >
                <span>
                  Ln {props.cursorPos.line}, Col {props.cursorPos.column}
                  {props.cursorPos.selectedChars > 0 ? ` (${props.cursorPos.selectedChars} selected)` : ''}
                </span>
              </div>

              {/* Indentation */}
              <div
                style={itemStyle}
                onClick={() => emit('changeIndent')}
                title="Select Indentation"
              >
                <span>Spaces: {props.activeTab.indentSize || 2}</span>
              </div>

              {/* Encoding */}
              <div
                style={itemStyle}
                onClick={() => emit('changeEncoding')}
                title="Select Encoding"
              >
                <span>{props.activeTab.encoding || 'UTF-8'}</span>
              </div>

              {/* End of line */}
              <div
                style={itemStyle}
                onClick={() => emit('changeEOL')}
                title="Select End of Line Sequence"
              >
                <span>{props.activeTab.eol || 'LF'}</span>
              </div>

              {/* Language Mode */}
              <div
                style={{
                  ...itemStyle,
                  fontWeight: 600,
                  color: 'var(--text-strong, #FFFFFF)',
                }}
                onClick={() => emit('formatDocument')}
                title="Format Document / Language"
              >
                <span>{props.activeTab.language?.toUpperCase() || 'TEXT'}</span>
              </div>
            </>
          )}
        </div>
      </div>
    )
  },
})
