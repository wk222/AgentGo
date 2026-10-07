import { defineComponent, ref, computed, type PropType } from 'vue'
import { parseOutline } from '../utils/outlineParser'
import type { OpenTab, OutlineSymbol } from '../types'

export const OutlinePanel = defineComponent({
  name: 'OutlinePanel',
  props: {
    activeTab: { type: Object as PropType<OpenTab | null>, default: null },
  },
  emits: ['jumpToLine'],
  setup(props, { emit }) {
    const filterText = ref('')

    const symbols = computed<OutlineSymbol[]>(() => {
      if (!props.activeTab || !props.activeTab.content) return []
      return parseOutline(props.activeTab.content, props.activeTab.language)
    })

    const filteredSymbols = computed(() => {
      const q = filterText.value.trim().toLowerCase()
      if (!q) return symbols.value
      return symbols.value.filter((s) => s.name.toLowerCase().includes(q))
    })

    const getSymbolBadge = (kind: OutlineSymbol['kind']) => {
      switch (kind) {
        case 'class':
          return { label: 'C', color: '#F59E0B', bg: '#FEF3C7' }
        case 'interface':
          return { label: 'I', color: '#3B82F6', bg: '#DBEAFE' }
        case 'function':
        case 'method':
          return { label: 'f', color: '#8B5CF6', bg: '#EDE9FE' }
        case 'enum':
          return { label: 'E', color: '#10B981', bg: '#D1FAE5' }
        case 'heading':
          return { label: 'H', color: '#6B7280', bg: '#F3F4F6' }
        default:
          return { label: 'v', color: '#06B6D4', bg: '#CFFAFE' }
      }
    }

    return {
      filterText,
      symbols,
      filteredSymbols,
      getSymbolBadge,
      onSelectSymbol: (sym: OutlineSymbol) => {
        emit('jumpToLine', sym.line)
      },
    }
  },
  render() {
    return (
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          height: '100%',
          backgroundColor: 'var(--sidebar, #18181B)',
          color: 'var(--text, #D4D4D8)',
          fontSize: '12px',
          overflow: 'hidden',
        }}
      >
        {/* Header */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '8px 12px 6px',
            fontSize: '11px',
            fontWeight: 700,
            textTransform: 'uppercase',
            letterSpacing: '0.5px',
            color: 'var(--text-dim, #71717A)',
            borderBottom: '1px solid var(--border-subtle, rgba(255,255,255,0.06))',
          }}
        >
          <span>代码大纲 (OUTLINE)</span>
          {this.activeTab && (
            <span
              style={{
                fontSize: '10px',
                color: '#A1A1AA',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
                maxWidth: '120px',
              }}
            >
              {this.activeTab.name}
            </span>
          )}
        </div>

        {/* Filter input */}
        {this.symbols.length > 5 && (
          <div style={{ padding: '6px 10px', borderBottom: '1px solid rgba(255,255,255,0.04)' }}>
            <input
              type="text"
              value={this.filterText}
              onInput={(e) => { this.filterText = (e.target as HTMLInputElement).value }}
              placeholder="过滤符号..."
              style={{
                width: '100%',
                backgroundColor: '#27272A',
                border: '1px solid rgba(255,255,255,0.08)',
                borderRadius: '3px',
                color: '#FFFFFF',
                fontSize: '11px',
                padding: '3px 6px',
                boxSizing: 'border-box',
                outline: 'none',
              }}
            />
          </div>
        )}

        {/* Symbols list */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '4px 0' }}>
          {!this.activeTab ? (
            <div style={{ padding: '32px 16px', textAlign: 'center', color: '#71717A', fontSize: '12px' }}>
              未打开任何文件
            </div>
          ) : this.filteredSymbols.length === 0 ? (
            <div style={{ padding: '32px 16px', textAlign: 'center', color: '#71717A', fontSize: '12px' }}>
              当前文件中未解析到符号
            </div>
          ) : (
            this.filteredSymbols.map((sym, idx) => {
              const badge = this.getSymbolBadge(sym.kind)
              return (
                <div
                  key={`${sym.name}_${sym.line}_${idx}`}
                  onClick={() => this.onSelectSymbol(sym)}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '6px',
                    padding: '4px 12px',
                    cursor: 'pointer',
                    fontSize: '12px',
                    color: '#E4E4E7',
                  }}
                  onMouseenter={(e) => {
                    (e.currentTarget as HTMLElement).style.backgroundColor = 'rgba(255,255,255,0.04)'
                  }}
                  onMouseleave={(e) => {
                    (e.currentTarget as HTMLElement).style.backgroundColor = 'transparent'
                  }}
                >
                  <span
                    style={{
                      display: 'inline-flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      width: '16px',
                      height: '16px',
                      borderRadius: '3px',
                      fontSize: '10px',
                      fontWeight: 700,
                      backgroundColor: badge.bg,
                      color: badge.color,
                      flexShrink: 0,
                    }}
                  >
                    {badge.label}
                  </span>
                  <span
                    style={{
                      flex: 1,
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {sym.name}
                  </span>
                  <span style={{ fontSize: '10px', color: '#71717A' }}>
                    :{sym.line}
                  </span>
                </div>
              )
            })
          )}
        </div>
      </div>
    )
  },
})
