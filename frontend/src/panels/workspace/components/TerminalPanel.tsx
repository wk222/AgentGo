import { defineComponent, ref, onMounted, onUnmounted, nextTick, type PropType } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { wailsCall, waitForWailsEvents } from '../../../wails'

interface TerminalTab {
  id: string
  title: string
  term?: Terminal
  fitAddon?: FitAddon
  container?: HTMLDivElement
  exited?: boolean
}

export const TerminalPanel = defineComponent({
  name: 'TerminalPanel',
  props: {
    height: { type: Number, default: 240 },
    workspaceRoot: { type: String, default: '' },
  },
  emits: ['close', 'resize'],
  setup(props, { emit }) {
    const terminals = ref<TerminalTab[]>([])
    const activeTabId = ref<string>('')
    const panelHeight = ref(props.height || 240)
    const isMaximized = ref(false)
    const savedHeight = ref(240)
    const isDragging = ref(false)
    const wrapperRef = ref<HTMLDivElement | null>(null)

    let resizeObserver: ResizeObserver | null = null
    let cleanupData: (() => void) | null = null
    let cleanupExit: (() => void) | null = null

    const handleFitActive = () => {
      const activeTab = terminals.value.find((t) => t.id === activeTabId.value)
      if (activeTab?.term && activeTab?.fitAddon && activeTab.container) {
        try {
          activeTab.fitAddon.fit()
          const { cols, rows } = activeTab.term
          if (cols > 0 && rows > 0) {
            wailsCall('TerminalResize', activeTab.id, cols, rows)
          }
        } catch (e) {
          console.debug('[TerminalPanel] fit error:', e)
        }
      }
    }

    const initXtermForTab = (tab: TerminalTab) => {
      if (!tab.container || tab.term) return

      const term = new Terminal({
        cursorBlink: true,
        cursorStyle: 'block',
        fontSize: 12,
        fontFamily: "'Fira Code', 'Cascadia Code', 'JetBrains Mono', 'Consolas', monospace",
        lineHeight: 1.25,
        scrollback: 5000,
        theme: {
          background: '#121214',
          foreground: '#d4d4d8',
          cursor: '#38bdf8',
          selectionBackground: 'rgba(56, 189, 248, 0.3)',
          black: '#18181b',
          red: '#ef4444',
          green: '#10b981',
          yellow: '#f59e0b',
          blue: '#3b82f6',
          magenta: '#a855f7',
          cyan: '#06b6d4',
          white: '#f4f4f5',
          brightBlack: '#71717a',
          brightRed: '#f87171',
          brightGreen: '#34d399',
          brightYellow: '#fbbf24',
          brightBlue: '#60a5fa',
          brightMagenta: '#c084fc',
          brightCyan: '#22d3ee',
          brightWhite: '#ffffff',
        },
      })

      const fitAddon = new FitAddon()
      term.loadAddon(fitAddon)
      term.open(tab.container)
      tab.term = term
      tab.fitAddon = fitAddon

      term.onData((data) => {
        wailsCall('TerminalWrite', tab.id, data)
      })

      nextTick(() => {
        try {
          fitAddon.fit()
          if (term.cols > 0 && term.rows > 0) {
            wailsCall('TerminalResize', tab.id, term.cols, term.rows)
          }
        } catch {}
      })
    }

    const switchTab = (id: string) => {
      activeTabId.value = id
      nextTick(() => {
        const tab = terminals.value.find((t) => t.id === id)
        if (tab?.term) {
          tab.term.focus()
          handleFitActive()
        }
      })
    }

    const createTab = async (shell?: string) => {
      const termId = `term-${Date.now()}-${Math.floor(Math.random() * 1000)}`
      const info = await wailsCall<{ id: string; shell: string; title: string }>(
        'TerminalCreate',
        termId,
        shell || '',
        props.workspaceRoot || '',
        120,
        30
      )
      const id = info?.id || termId
      const title = info?.title
        ? info.title.split(/[\/\\]/).pop() || 'Terminal'
        : shell || 'Terminal'

      const tab: TerminalTab = {
        id,
        title,
        exited: false,
      }
      terminals.value.push(tab)
      activeTabId.value = id

      nextTick(() => {
        initXtermForTab(tab)
        tab.term?.focus()
      })
    }

    const closeTab = async (id: string, e?: MouseEvent) => {
      if (e) e.stopPropagation()
      await wailsCall('TerminalClose', id)
      const idx = terminals.value.findIndex((t) => t.id === id)
      if (idx !== -1) {
        const tab = terminals.value[idx]
        tab.term?.dispose()
        terminals.value.splice(idx, 1)
        if (activeTabId.value === id) {
          if (terminals.value.length > 0) {
            const nextIdx = Math.max(0, idx - 1)
            switchTab(terminals.value[nextIdx].id)
          } else {
            createTab()
          }
        }
      }
    }

    const sendQuickCommand = (cmd: string) => {
      const activeTab = terminals.value.find((t) => t.id === activeTabId.value)
      if (activeTab) {
        wailsCall('TerminalWrite', activeTab.id, cmd + '\r')
        activeTab.term?.focus()
      }
    }

    const sendInterrupt = () => {
      const activeTab = terminals.value.find((t) => t.id === activeTabId.value)
      if (activeTab) {
        wailsCall('TerminalWrite', activeTab.id, '\x03')
        activeTab.term?.focus()
      }
    }

    const clearActive = () => {
      const activeTab = terminals.value.find((t) => t.id === activeTabId.value)
      activeTab?.term?.clear()
      activeTab?.term?.focus()
    }

    const startDrag = (e: MouseEvent) => {
      e.preventDefault()
      isDragging.value = true
      const startY = e.clientY
      const startH = panelHeight.value

      const onMouseMove = (moveEvent: MouseEvent) => {
        const delta = startY - moveEvent.clientY
        const newH = Math.max(120, Math.min(window.innerHeight - 150, startH + delta))
        panelHeight.value = newH
      }

      const onMouseUp = () => {
        isDragging.value = false
        window.removeEventListener('mousemove', onMouseMove)
        window.removeEventListener('mouseup', onMouseUp)
        nextTick(() => handleFitActive())
      }

      window.addEventListener('mousemove', onMouseMove)
      window.addEventListener('mouseup', onMouseUp)
    }

    const toggleMaximize = () => {
      if (isMaximized.value) {
        panelHeight.value = savedHeight.value
        isMaximized.value = false
      } else {
        savedHeight.value = panelHeight.value
        panelHeight.value = Math.max(380, Math.floor(window.innerHeight * 0.65))
        isMaximized.value = true
      }
      nextTick(() => handleFitActive())
    }

    onMounted(async () => {
      if (wrapperRef.value) {
        resizeObserver = new ResizeObserver(() => {
          handleFitActive()
        })
        resizeObserver.observe(wrapperRef.value)
      }

      const evts = await waitForWailsEvents(2500)
      if (evts?.On) {
        cleanupData = evts.On('terminal:data', (payload: any) => {
          const termId = payload?.term_id || payload?.id
          const data = payload?.data
          if (!termId || data == null) return
          const tab = terminals.value.find((t) => t.id === termId)
          if (tab?.term) {
            tab.term.write(data)
          }
        })
        cleanupExit = evts.On('terminal:exit', (payload: any) => {
          const termId = payload?.term_id || payload?.id
          const exitCode = payload?.exit_code ?? 0
          const tab = terminals.value.find((t) => t.id === termId)
          if (tab?.term) {
            tab.term.write(`\r\n\x1b[90m[Process exited with code ${exitCode}]\x1b[0m\r\n`)
            tab.exited = true
          }
        })
      }

      try {
        const list = await wailsCall<any[]>('TerminalList')
        if (Array.isArray(list) && list.length > 0) {
          for (const item of list) {
            const title = item.title
              ? item.title.split(/[\/\\]/).pop() || 'Terminal'
              : 'Terminal'
            terminals.value.push({
              id: item.id,
              title,
              exited: false,
            })
          }
          activeTabId.value = terminals.value[0].id
          nextTick(() => {
            terminals.value.forEach((t) => initXtermForTab(t))
          })
        } else {
          await createTab()
        }
      } catch {
        await createTab()
      }
    })

    onUnmounted(() => {
      cleanupData?.()
      cleanupExit?.()
      resizeObserver?.disconnect()
      terminals.value.forEach((t) => t.term?.dispose())
    })

    return {
      terminals,
      activeTabId,
      panelHeight,
      isMaximized,
      isDragging,
      wrapperRef,
      switchTab,
      createTab,
      closeTab,
      sendQuickCommand,
      sendInterrupt,
      clearActive,
      startDrag,
      toggleMaximize,
      initXtermForTab,
    }
  },
  render() {
    return (
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          height: `${this.panelHeight}px`,
          backgroundColor: '#121214',
          borderTop: '1px solid var(--border-subtle, rgba(255,255,255,0.08))',
          color: '#E4E4E7',
          overflow: 'hidden',
          flexShrink: 0,
          position: 'relative',
        }}
      >
        {/* Top Border Drag Resizer */}
        <div
          onMousedown={this.startDrag}
          title="拖拽调整终端高度"
          style={{
            position: 'absolute',
            top: 0,
            left: 0,
            right: 0,
            height: '4px',
            cursor: 'ns-resize',
            zIndex: 10,
            backgroundColor: this.isDragging ? '#38BDF8' : 'transparent',
            transition: 'background-color 0.15s ease',
          }}
        />

        {/* Terminal Header Bar */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '2px 8px',
            backgroundColor: '#18181B',
            borderBottom: '1px solid rgba(255,255,255,0.06)',
            fontSize: '11px',
            userSelect: 'none',
            minHeight: '28px',
          }}
        >
          {/* Terminal Tabs */}
          <div style={{ display: 'flex', alignItems: 'center', gap: '4px', overflowX: 'auto' }}>
            <span style={{ fontWeight: 700, color: '#38BDF8', marginRight: '6px' }}>TERMINAL</span>
            {this.terminals.map((tab, idx) => {
              const isActive = tab.id === this.activeTabId
              return (
                <div
                  key={tab.id}
                  onClick={() => this.switchTab(tab.id)}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '6px',
                    padding: '2px 8px',
                    borderRadius: '4px',
                    backgroundColor: isActive ? '#27272A' : 'transparent',
                    color: isActive ? '#F4F4F5' : '#71717A',
                    cursor: 'pointer',
                    fontSize: '11px',
                    fontWeight: isActive ? 600 : 400,
                    border: isActive
                      ? '1px solid rgba(255,255,255,0.1)'
                      : '1px solid transparent',
                  }}
                >
                  <span>{idx + 1}: {tab.title}</span>
                  {tab.exited && (
                    <span style={{ color: '#EF4444', fontSize: '9px' }}>[已退出]</span>
                  )}
                  <span
                    onClick={(e) => this.closeTab(tab.id, e)}
                    title="关闭终端"
                    style={{
                      cursor: 'pointer',
                      opacity: 0.6,
                      fontSize: '10px',
                      padding: '0 2px',
                    }}
                    onMouseenter={(e) => { (e.target as HTMLElement).style.opacity = '1' }}
                    onMouseleave={(e) => { (e.target as HTMLElement).style.opacity = '0.6' }}
                  >
                    ✕
                  </span>
                </div>
              )
            })}

            {/* New Terminal Tab Button */}
            <button
              onClick={() => this.createTab()}
              title="新建终端"
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                padding: '2px 6px',
                backgroundColor: 'rgba(255,255,255,0.05)',
                color: '#A1A1AA',
                border: 'none',
                borderRadius: '3px',
                cursor: 'pointer',
                fontSize: '11px',
                marginLeft: '2px',
              }}
            >
              +
            </button>
          </div>

          {/* Right Header Actions */}
          <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
            {/* Preset quick command buttons */}
            <button
              onClick={() => this.sendQuickCommand('git status')}
              style={{
                padding: '1px 6px',
                backgroundColor: 'rgba(255,255,255,0.05)',
                color: '#A1A1AA',
                border: 'none',
                borderRadius: '3px',
                cursor: 'pointer',
                fontSize: '10px',
              }}
            >
              git status
            </button>
            <button
              onClick={() => this.sendQuickCommand('go test ./...')}
              style={{
                padding: '1px 6px',
                backgroundColor: 'rgba(255,255,255,0.05)',
                color: '#A1A1AA',
                border: 'none',
                borderRadius: '3px',
                cursor: 'pointer',
                fontSize: '10px',
              }}
            >
              go test
            </button>
            <button
              onClick={() => this.sendQuickCommand('npm run build')}
              style={{
                padding: '1px 6px',
                backgroundColor: 'rgba(255,255,255,0.05)',
                color: '#A1A1AA',
                border: 'none',
                borderRadius: '3px',
                cursor: 'pointer',
                fontSize: '10px',
              }}
            >
              npm build
            </button>

            {/* Interrupt Ctrl+C */}
            <button
              onClick={this.sendInterrupt}
              title="发送中断信号 (Ctrl+C)"
              style={{
                padding: '1px 6px',
                backgroundColor: 'rgba(239, 68, 68, 0.1)',
                color: '#F87171',
                border: '1px solid rgba(239, 68, 68, 0.2)',
                borderRadius: '3px',
                cursor: 'pointer',
                fontSize: '10px',
              }}
            >
              ^C 中断
            </button>

            {/* Clear Terminal */}
            <button
              onClick={this.clearActive}
              title="清屏"
              style={{
                padding: '1px 6px',
                backgroundColor: 'transparent',
                color: '#71717A',
                border: 'none',
                cursor: 'pointer',
                fontSize: '11px',
              }}
            >
              ⊘ 清屏
            </button>

            {/* Maximize / Restore Toggle */}
            <button
              onClick={this.toggleMaximize}
              title={this.isMaximized ? '还原高度' : '最大化终端'}
              style={{
                padding: '1px 6px',
                backgroundColor: 'transparent',
                color: '#71717A',
                border: 'none',
                cursor: 'pointer',
                fontSize: '11px',
              }}
            >
              {this.isMaximized ? '🗗' : '⤢'}
            </button>

            {/* Hide Terminal Drawer */}
            <button
              onClick={() => this.$emit('close')}
              title="隐藏终端"
              style={{
                padding: '1px 6px',
                backgroundColor: 'transparent',
                color: '#71717A',
                border: 'none',
                cursor: 'pointer',
                fontSize: '12px',
              }}
            >
              ✕
            </button>
          </div>
        </div>

        {/* Xterm Terminal Containers Wrapper */}
        <div
          ref="wrapperRef"
          style={{
            flex: 1,
            position: 'relative',
            overflow: 'hidden',
            backgroundColor: '#121214',
            padding: '4px',
          }}
        >
          {this.terminals.map((tab) => {
            const isTabActive = tab.id === this.activeTabId
            return (
              <div
                key={tab.id}
                ref={(el) => {
                  if (el) {
                    tab.container = el as HTMLDivElement
                    this.initXtermForTab(tab)
                  }
                }}
                style={{
                  position: 'absolute',
                  inset: '4px',
                  display: isTabActive ? 'block' : 'none',
                  overflow: 'hidden',
                }}
              />
            )
          })}
        </div>
      </div>
    )
  },
})
