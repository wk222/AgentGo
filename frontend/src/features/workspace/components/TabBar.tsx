import { defineComponent, ref, reactive, type PropType } from 'vue'
import type { OpenTab } from '../types'
import { FileIcon } from '../utils/icons'

export const TabBar = defineComponent({
  name: 'TabBar',
  props: {
    tabs: { type: Array as PropType<OpenTab[]>, required: true },
    activeTabPath: { type: String, required: true },
  },
  emits: ['selectTab', 'closeTab', 'closeOtherTabs', 'closeAllTabs'],
  setup(props, { emit }) {
    const contextMenu = reactive<{
      visible: boolean
      x: number
      y: number
      targetTab: OpenTab | null
    }>({
      visible: false,
      x: 0,
      y: 0,
      targetTab: null,
    })

    const onTabContextMenu = (e: MouseEvent, tab: OpenTab) => {
      e.preventDefault()
      contextMenu.visible = true
      contextMenu.x = e.clientX
      contextMenu.y = e.clientY
      contextMenu.targetTab = tab
    }

    const closeContextMenu = () => {
      contextMenu.visible = false
      contextMenu.targetTab = null
    }

    const copyPath = (path: string) => {
      navigator.clipboard?.writeText(path)
      closeContextMenu()
    }

    return () => (
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          height: '35px',
          backgroundColor: 'var(--surface, #1E1E22)',
          borderBottom: '1px solid var(--border-subtle, rgba(255,255,255,0.08))',
          overflowX: 'auto',
          overflowY: 'hidden',
          scrollbarWidth: 'none',
          userSelect: 'none',
          flexShrink: 0,
        }}
        onClick={closeContextMenu}
      >
        {props.tabs.map((tab) => {
          const isActive = tab.path === props.activeTabPath
          return (
            <div
              key={tab.path}
              onClick={() => emit('selectTab', tab.path)}
              onAuxclick={(e) => {
                if (e.button === 1) {
                  // Middle click closes tab
                  e.preventDefault()
                  emit('closeTab', tab.path)
                }
              }}
              onContextmenu={(e) => onTabContextMenu(e, tab)}
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: '6px',
                padding: '0 10px',
                height: '100%',
                backgroundColor: isActive ? 'var(--bg, #18181B)' : 'transparent',
                borderRight: '1px solid var(--border-subtle, rgba(255,255,255,0.06))',
                borderTop: isActive ? '2px solid var(--accent, #38BDF8)' : '2px solid transparent',
                color: isActive ? 'var(--text-strong, #FFFFFF)' : 'var(--text-dim, #A1A1AA)',
                fontSize: '12px',
                fontFamily: 'var(--font-ui, sans-serif)',
                cursor: 'pointer',
                transition: 'background-color 0.15s ease',
                minWidth: '100px',
                maxWidth: '200px',
                position: 'relative',
              }}
              title={tab.path}
            >
              <FileIcon fileName={tab.name} isDir={false} size={14} />
              
              <span
                style={{
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                  flex: 1,
                  fontWeight: isActive ? 600 : 400,
                  fontStyle: tab.isDirty ? 'italic' : 'normal',
                }}
              >
                {tab.name}
              </span>

              {/* Dirty Indicator / Close Button */}
              <div
                style={{
                  width: '16px',
                  height: '16px',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  borderRadius: '3px',
                  marginLeft: '2px',
                  flexShrink: 0,
                }}
                onClick={(e) => {
                  e.stopPropagation()
                  emit('closeTab', tab.path)
                }}
                onMouseenter={(e) => {
                  ;(e.currentTarget as HTMLElement).style.backgroundColor = 'rgba(255,255,255,0.1)'
                }}
                onMouseleave={(e) => {
                  ;(e.currentTarget as HTMLElement).style.backgroundColor = 'transparent'
                }}
              >
                {tab.isDirty ? (
                  <span
                    style={{
                      width: '7px',
                      height: '7px',
                      borderRadius: '50%',
                      backgroundColor: 'var(--accent, #38BDF8)',
                    }}
                  />
                ) : (
                  <span style={{ fontSize: '13px', lineHeight: '1', opacity: 0.6 }}>×</span>
                )}
              </div>
            </div>
          )
        })}

        {/* Tab Context Menu */}
        {contextMenu.visible && contextMenu.targetTab && (
          <div
            style={{
              position: 'fixed',
              top: `${contextMenu.y}px`,
              left: `${contextMenu.x}px`,
              backgroundColor: 'var(--surface, #27272A)',
              border: '1px solid var(--border, rgba(255,255,255,0.15))',
              borderRadius: '6px',
              padding: '4px 0',
              zIndex: 9999,
              boxShadow: '0 8px 24px rgba(0,0,0,0.5)',
              minWidth: '160px',
              fontSize: '12px',
            }}
          >
            <div
              style={{ padding: '6px 12px', cursor: 'pointer', color: 'var(--text, #E4E4E7)' }}
              onClick={() => {
                emit('closeTab', contextMenu.targetTab!.path)
                closeContextMenu()
              }}
            >
              关闭标签
            </div>
            <div
              style={{ padding: '6px 12px', cursor: 'pointer', color: 'var(--text, #E4E4E7)' }}
              onClick={() => {
                emit('closeOtherTabs', contextMenu.targetTab!.path)
                closeContextMenu()
              }}
            >
              关闭其他标签
            </div>
            <div
              style={{ padding: '6px 12px', cursor: 'pointer', color: 'var(--text, #E4E4E7)' }}
              onClick={() => {
                emit('closeAllTabs')
                closeContextMenu()
              }}
            >
              关闭所有标签
            </div>
            <div style={{ height: '1px', backgroundColor: 'rgba(255,255,255,0.08)', margin: '4px 0' }} />
            <div
              style={{ padding: '6px 12px', cursor: 'pointer', color: 'var(--text, #E4E4E7)' }}
              onClick={() => copyPath(contextMenu.targetTab!.path)}
            >
              复制相对路径
            </div>
          </div>
        )}
      </div>
    )
  },
})
