import { defineComponent, computed, type PropType } from 'vue'

export const Breadcrumbs = defineComponent({
  name: 'Breadcrumbs',
  props: {
    filePath: { type: String, default: '' },
    workspaceRoot: { type: String, default: '' },
    currentSymbol: { type: String, default: '' },
  },
  emits: ['navigate'],
  setup(props, { emit }) {
    const segments = computed(() => {
      if (!props.filePath) return []
      return props.filePath.split('/').filter(Boolean)
    })

    return () => {
      if (!props.filePath) return null

      return (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '6px',
            padding: '4px 12px',
            fontSize: '11px',
            fontFamily: 'var(--font-ui, sans-serif)',
            color: 'var(--text-dim, #71717A)',
            backgroundColor: 'var(--surface, #1E1E22)',
            borderBottom: '1px solid var(--border-subtle, rgba(255,255,255,0.06))',
            userSelect: 'none',
            overflowX: 'auto',
            whiteSpace: 'nowrap',
          }}
        >
          <span style={{ display: 'flex', alignItems: 'center', gap: '4px', opacity: 0.8 }}>
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
            </svg>
            <span>workspace</span>
          </span>

          {segments.value.map((seg, idx) => {
            const isLast = idx === segments.value.length - 1
            const subPath = segments.value.slice(0, idx + 1).join('/')
            return (
              <span key={subPath} style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                <span style={{ opacity: 0.4 }}>/</span>
                <span
                  onClick={() => emit('navigate', subPath, isLast)}
                  style={{
                    color: isLast ? 'var(--text-strong, #FFFFFF)' : 'var(--text-dim, #A1A1AA)',
                    fontWeight: isLast ? 600 : 400,
                    cursor: isLast ? 'default' : 'pointer',
                    textDecoration: 'none',
                  }}
                  onMouseenter={(e) => {
                    if (!isLast) (e.target as HTMLElement).style.textDecoration = 'underline'
                  }}
                  onMouseleave={(e) => {
                    if (!isLast) (e.target as HTMLElement).style.textDecoration = 'none'
                  }}
                >
                  {seg}
                </span>
              </span>
            )
          })}

          {props.currentSymbol && (
            <span style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
              <span style={{ opacity: 0.4 }}>&gt;</span>
              <span style={{ color: 'var(--accent, #38BDF8)', fontWeight: 500 }}>
                {props.currentSymbol}
              </span>
            </span>
          )}
        </div>
      )
    }
  },
})
