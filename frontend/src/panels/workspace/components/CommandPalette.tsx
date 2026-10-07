import { defineComponent, ref, computed, onMounted, onUnmounted, nextTick, type PropType } from 'vue'
import type { WorkspaceFileItem, CommandItem } from '../types'
import { FileIcon } from '../utils/icons'

export const CommandPalette = defineComponent({
  name: 'CommandPalette',
  props: {
    visible: { type: Boolean, required: true },
    mode: { type: String as PropType<'file' | 'command'>, default: 'file' },
    allFiles: { type: Array as PropType<WorkspaceFileItem[]>, default: () => [] },
    commands: { type: Array as PropType<CommandItem[]>, default: () => [] },
  },
  emits: ['close', 'selectFile', 'executeCommand'],
  setup(props, { emit }) {
    const searchInput = ref<HTMLInputElement | null>(null)
    const query = ref('')
    const selectedIndex = ref(0)

    // Flat file list for fuzzy search
    const flatFiles = computed(() => {
      const list: WorkspaceFileItem[] = []
      const traverse = (items: WorkspaceFileItem[]) => {
        for (const item of items) {
          if (!item.is_dir) {
            list.push(item)
          }
          if (item.children && item.children.length) {
            traverse(item.children)
          }
        }
      }
      traverse(props.allFiles)
      return list
    })

    const filteredFiles = computed(() => {
      const q = query.value.trim().toLowerCase()
      if (!q) return flatFiles.value.slice(0, 30)
      return flatFiles.value
        .filter((f) => f.name.toLowerCase().includes(q) || f.path.toLowerCase().includes(q))
        .slice(0, 30)
    })

    const filteredCommands = computed(() => {
      const q = query.value.trim().toLowerCase()
      if (!q) return props.commands
      return props.commands.filter((c) =>
        c.title.toLowerCase().includes(q) ||
        (c.category && c.category.toLowerCase().includes(q)) ||
        (c.shortcut && c.shortcut.toLowerCase().includes(q))
      )
    })

    const currentListCount = computed(() => {
      return props.mode === 'file' ? filteredFiles.value.length : filteredCommands.value.length
    })

    const handleKeydown = (e: KeyboardEvent) => {
      if (!props.visible) return

      if (e.key === 'ArrowDown') {
        e.preventDefault()
        if (currentListCount.value > 0) {
          selectedIndex.value = (selectedIndex.value + 1) % currentListCount.value
        }
      } else if (e.key === 'ArrowUp') {
        e.preventDefault()
        if (currentListCount.value > 0) {
          selectedIndex.value = (selectedIndex.value - 1 + currentListCount.value) % currentListCount.value
        }
      } else if (e.key === 'Enter') {
        e.preventDefault()
        if (props.mode === 'file' && filteredFiles.value[selectedIndex.value]) {
          emit('selectFile', filteredFiles.value[selectedIndex.value])
          emit('close')
        } else if (props.mode === 'command' && filteredCommands.value[selectedIndex.value]) {
          emit('executeCommand', filteredCommands.value[selectedIndex.value])
          emit('close')
        }
      } else if (e.key === 'Escape') {
        e.preventDefault()
        emit('close')
      }
    }

    onMounted(() => {
      window.addEventListener('keydown', handleKeydown)
    })

    onUnmounted(() => {
      window.removeEventListener('keydown', handleKeydown)
    })

    return {
      searchInput,
      query,
      selectedIndex,
      filteredFiles,
      filteredCommands,
      currentListCount,
      onSelectFile: (file: WorkspaceFileItem) => {
        emit('selectFile', file)
        emit('close')
      },
      onExecuteCommand: (cmd: CommandItem) => {
        emit('executeCommand', cmd)
        emit('close')
      },
    }
  },
  render() {
    if (!this.visible) return null

    return (
      <div
        style={{
          position: 'fixed',
          top: 0,
          left: 0,
          right: 0,
          bottom: 0,
          backgroundColor: 'rgba(0, 0, 0, 0.55)',
          backdropFilter: 'blur(3px)',
          zIndex: 1000,
          display: 'flex',
          justifyContent: 'center',
          alignItems: 'flex-start',
          paddingTop: '60px',
        }}
        onClick={() => this.$emit('close')}
      >
        <div
          style={{
            width: '560px',
            maxWidth: '92vw',
            maxHeight: '440px',
            backgroundColor: '#1E1E22',
            border: '1px solid rgba(255, 255, 255, 0.12)',
            borderRadius: '10px',
            boxShadow: '0 16px 36px rgba(0, 0, 0, 0.55)',
            display: 'flex',
            flexDirection: 'column',
            overflow: 'hidden',
          }}
          onClick={(e) => e.stopPropagation()}
        >
          {/* Top Search Input Box */}
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '8px',
              padding: '10px 14px',
              borderBottom: '1px solid rgba(255, 255, 255, 0.08)',
              backgroundColor: '#18181B',
            }}
          >
            <span style={{ color: 'var(--accent, #38BDF8)', fontSize: '13px' }}>
              {this.mode === 'file' ? '📂' : '⚡'}
            </span>
            <input
              ref="searchInput"
              type="text"
              value={this.query}
              onInput={(e) => {
                this.query = (e.target as HTMLInputElement).value
                this.selectedIndex = 0
              }}
              placeholder={this.mode === 'file' ? '键入文件名进行快速查找 (Ctrl+P)...' : '键入命令或操作名称 (Ctrl+Shift+P)...'}
              autofocus
              style={{
                flex: 1,
                border: 'none',
                outline: 'none',
                backgroundColor: 'transparent',
                color: '#FFFFFF',
                fontSize: '13px',
                fontFamily: 'var(--font-ui, sans-serif)',
              }}
            />
            {this.query && (
              <button
                onClick={() => { this.query = ''; this.selectedIndex = 0 }}
                style={{
                  background: 'none',
                  border: 'none',
                  color: '#71717A',
                  cursor: 'pointer',
                  fontSize: '12px',
                }}
              >
                ✕
              </button>
            )}
          </div>

          {/* List Results */}
          <div
            style={{
              flex: 1,
              overflowY: 'auto',
              padding: '6px 0',
            }}
          >
            {this.mode === 'file' ? (
              this.filteredFiles.length > 0 ? (
                this.filteredFiles.map((file, idx) => {
                  const isSel = idx === this.selectedIndex
                  return (
                    <div
                      key={file.path}
                      onClick={() => this.onSelectFile(file)}
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: '8px',
                        padding: '6px 14px',
                        backgroundColor: isSel ? 'rgba(56, 189, 248, 0.15)' : 'transparent',
                        color: isSel ? '#FFFFFF' : '#D4D4D8',
                        cursor: 'pointer',
                        fontSize: '12px',
                        borderLeft: isSel ? '2px solid var(--accent, #38BDF8)' : '2px solid transparent',
                      }}
                      onMouseenter={() => { this.selectedIndex = idx }}
                    >
                      <FileIcon fileName={file.name} isDir={false} size={14} />
                      <span style={{ fontWeight: 500, color: isSel ? '#FFFFFF' : '#E4E4E7' }}>{file.name}</span>
                      <span style={{ marginLeft: 'auto', color: '#71717A', fontSize: '11px' }}>{file.path}</span>
                    </div>
                  )
                })
              ) : (
                <div style={{ padding: '24px 0', textAlign: 'center', color: '#71717A', fontSize: '12px' }}>
                  未找到匹配的文件
                </div>
              )
            ) : (
              this.filteredCommands.length > 0 ? (
                this.filteredCommands.map((cmd, idx) => {
                  const isSel = idx === this.selectedIndex
                  return (
                    <div
                      key={cmd.id}
                      onClick={() => this.onExecuteCommand(cmd)}
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: '8px',
                        padding: '6px 14px',
                        backgroundColor: isSel ? 'rgba(56, 189, 248, 0.15)' : 'transparent',
                        color: isSel ? '#FFFFFF' : '#D4D4D8',
                        cursor: 'pointer',
                        fontSize: '12px',
                        borderLeft: isSel ? '2px solid var(--accent, #38BDF8)' : '2px solid transparent',
                      }}
                      onMouseenter={() => { this.selectedIndex = idx }}
                    >
                      {cmd.category && (
                        <span style={{ color: '#71717A', fontSize: '11px', marginRight: '4px' }}>
                          {cmd.category}:
                        </span>
                      )}
                      <span style={{ fontWeight: 500, color: isSel ? '#FFFFFF' : '#E4E4E7' }}>{cmd.title}</span>
                      {cmd.shortcut && (
                        <kbd
                          style={{
                            marginLeft: 'auto',
                            padding: '1px 5px',
                            backgroundColor: '#27272A',
                            border: '1px solid rgba(255,255,255,0.1)',
                            borderRadius: '3px',
                            fontSize: '10px',
                            color: '#A1A1AA',
                            fontFamily: 'monospace',
                          }}
                        >
                          {cmd.shortcut}
                        </kbd>
                      )}
                    </div>
                  )
                })
              ) : (
                <div style={{ padding: '24px 0', textAlign: 'center', color: '#71717A', fontSize: '12px' }}>
                  未找到匹配的命令
                </div>
              )
            )}
          </div>

          {/* Footer Hints */}
          <div
            style={{
              padding: '6px 14px',
              backgroundColor: '#18181B',
              borderTop: '1px solid rgba(255, 255, 255, 0.06)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              fontSize: '11px',
              color: '#71717A',
            }}
          >
            <span>↑↓ 导航 • ↵ 执行 • Esc 关闭</span>
            <span>AgentGo Workspace</span>
          </div>
        </div>
      </div>
    )
  },
})
