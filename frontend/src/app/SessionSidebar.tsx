import { defineComponent, ref, reactive, computed, nextTick, type PropType } from 'vue'
import { Plus, Search, MessageSquare, Trash2, X, Settings, PanelRight, AppWindow, FolderTree, Pencil, GitBranch } from 'lucide-vue-next'
import { sessionIdOf, sessionTitle, type Session } from '../stores/useChat'
import { forkLineage, lineageText } from '../stores/forkModel'
import { pinnedPages, generatedPageName } from '../stores/generatedPages'
import { exportSession } from '../stores/sessionExport'
import { VIEWS } from './viewRegistry'
import { useLayout } from '../stores/useLayout'
import { stageState } from '../stores/agentBridge'
import { notify } from '../shared/ui/notify'
import SessionExportDialog from '../features/chat/SessionExportDialog'
import '../features/chat/fork.css'

const MAX_SHOWN = 100

export default defineComponent({
  name: 'SessionSidebar',
  props: {
    pending: { type: Number, default: 0 },
    sessions: { type: Array as PropType<Session[]>, required: true },
    currentId: { type: String, default: '' },
    /** 各会话的运行标记：running 正在跑，awaiting 停在等用户处理。 */
    badges: { type: Object as PropType<Record<string, 'running' | 'awaiting'>>, default: () => ({}) },
    onNew: { type: Function as PropType<() => void>, required: true },
    onSelect: { type: Function as PropType<(id: string) => void>, required: true },
    onDelete: { type: Function as PropType<(id: string) => void>, default: undefined },
    onDeleteBatch: { type: Function as PropType<(ids: string[]) => void>, default: undefined },
    onDeleteOthers: { type: Function as PropType<(keepId?: string) => Promise<number> | void>, default: undefined },
    onDeleteByTitle: { type: Function as PropType<(title: string) => Promise<number> | void>, default: undefined },
    /** 重命名：返回实际保存的标题；失败时抛出带原因的错误。 */
    onRename: { type: Function as PropType<(id: string, title: string) => Promise<string>>, default: undefined },
    /** 分叉：整条复制成新对话并切过去（原对话不动）。 */
    onFork: { type: Function as PropType<(id: string) => void>, default: undefined },
  },
  setup(props) {
    const layout = useLayout()
    const openView = (id: string) => { if (id.startsWith('app:')) { stageState.activeAppName=id.slice(4); layout.openWorkbench('apps') } else layout.setView(id); if (window.innerWidth < 900) layout.state.sidebarOpen = false }
    const filter = ref('')
    const cleaning = ref(false)
    const ctxMenu = reactive<{ visible: boolean; x: number; y: number; sid: string; title: string }>({
      visible: false,
      x: 0,
      y: 0,
      sid: '',
      title: '',
    })

    const shown = computed(() => {
      const q = filter.value.toLowerCase().trim()
      const all = props.sessions
      const list = q ? all.filter(s => sessionTitle(s).toLowerCase().includes(q) || sessionIdOf(s).toLowerCase().includes(q)) : all
      return list.slice(0, MAX_SHOWN)
    })

    const titleCounts = computed(() => {
      const counts = new Map<string, number>()
      for (const s of props.sessions) {
        const t = sessionTitle(s)
        counts.set(t, (counts.get(t) || 0) + 1)
      }
      return counts
    })

    const closeMenu = () => {
      ctxMenu.visible = false
    }

    // 重命名：标题原地变成输入框。回车保存、Esc 放弃、点到别处也保存；输入法拼音还没上屏时的回车不算。
    const editing = reactive({ sid: '', value: '', busy: false })
    const editInput = ref<HTMLInputElement | null>(null)
    const cancelRename = () => { editing.sid = ''; editing.value = ''; editing.busy = false }
    const startRename = (sid: string, title: string) => {
      if (!props.onRename) return
      closeMenu()
      editing.sid = sid
      editing.value = title
      editing.busy = false
      void nextTick(() => { editInput.value?.focus(); editInput.value?.select() })
    }
    const commitRename = async () => {
      const sid = editing.sid
      if (!sid || editing.busy) return
      const next = editing.value.trim()
      const current = sessionTitle(props.sessions.find(s => sessionIdOf(s) === sid))
      if (!next || next === current) { cancelRename(); return }
      editing.busy = true
      try {
        await props.onRename?.(sid, next)
        cancelRename()
      } catch (e) {
        editing.busy = false
        notify.error(e instanceof Error ? e.message : '重命名失败')
        void nextTick(() => editInput.value?.focus())
      }
    }
    const handleFork = (sid: string, e?: MouseEvent) => {
      e?.stopPropagation()
      closeMenu()
      props.onFork?.(sid)
    }
    const handleExport = async (sid: string, e?: MouseEvent) => {
      e?.stopPropagation()
      closeMenu()
      try {
        await exportSession(sid)
      } catch (err) {
        notify.error(err instanceof Error ? err.message : '导出失败')
      }
    }

    const onContextMenu = (e: MouseEvent, s: Session) => {
      e.preventDefault()
      e.stopPropagation()
      ctxMenu.visible = true
      ctxMenu.x = Math.min(e.clientX, window.innerWidth - 210)
      ctxMenu.y = Math.min(e.clientY, window.innerHeight - 180)
      ctxMenu.sid = sessionIdOf(s)
      ctxMenu.title = sessionTitle(s)
    }

    const handleDeleteOne = (e: MouseEvent, sid: string) => {
      e.stopPropagation()
      closeMenu()
      props.onDelete?.(sid)
    }

    const handleCleanFiltered = async (e?: MouseEvent) => {
      e?.stopPropagation()
      closeMenu()
      const ids = shown.value.map(s => sessionIdOf(s)).filter(Boolean)
      if (!ids.length || cleaning.value) return
      cleaning.value = true
      try {
        if (props.onDeleteBatch) {
          await props.onDeleteBatch(ids)
        } else {
          ids.forEach(id => props.onDelete?.(id))
        }
        filter.value = ''
        notify.success(`已清理 ${ids.length} 条匹配对话`)
      } finally {
        cleaning.value = false
      }
    }

    const handleCleanOthers = async (keepSid?: string, e?: MouseEvent) => {
      e?.stopPropagation()
      closeMenu()
      if (cleaning.value) return
      const fallbackKeep = props.sessions.length > 0 ? sessionIdOf(props.sessions[0]) : ''
      const targetKeep = String(keepSid || props.currentId || fallbackKeep || '').trim()
      cleaning.value = true
      try {
        if (props.onDeleteOthers) {
          const n = await props.onDeleteOthers(targetKeep)
          notify.success(`已清空其余 ${typeof n === 'number' && n > 0 ? n : ''} 条历史对话`.replace('  ', ' '))
          return
        }
        const ids = props.sessions
          .map(s => sessionIdOf(s))
          .filter(id => id && id !== targetKeep)
        if (!ids.length) {
          notify.info('没有其他可清理的历史对话')
          return
        }
        if (props.onDeleteBatch) {
          await props.onDeleteBatch(ids)
        } else {
          ids.forEach(id => props.onDelete?.(id))
        }
        notify.success(`已清空其余 ${ids.length} 条历史对话`)
      } finally {
        cleaning.value = false
      }
    }

    const handleCleanDuplicateTitles = async (title: string, e?: MouseEvent) => {
      e?.stopPropagation()
      closeMenu()
      if (!title || cleaning.value) return
      cleaning.value = true
      try {
        if (props.onDeleteByTitle) {
          const n = await props.onDeleteByTitle(title)
          notify.success(`已批量删除 ${typeof n === 'number' && n > 0 ? n : ''} 条「${title.slice(0, 12)}」同名对话`)
          return
        }
        const ids = props.sessions
          .filter(s => sessionTitle(s) === title)
          .map(s => sessionIdOf(s))
          .filter(Boolean)
        if (!ids.length) return
        if (props.onDeleteBatch) {
          await props.onDeleteBatch(ids)
        } else {
          ids.forEach(id => props.onDelete?.(id))
        }
        notify.success(`已删除 ${ids.length} 条同名对话`)
      } finally {
        cleaning.value = false
      }
    }

    return () => (
      <aside class="sb" onClick={closeMenu}>
        <button class="sb-new-chat" type="button" onClick={props.onNew}>
          <Plus size={14} stroke-width={2.4} />
          <span>新对话</span>
        </button>
        <div class="sb-search-wrap">
          <Search class="sb-search-icon" size={13} />
          <input
            class="sb-search"
            placeholder="搜索对话…"
            aria-label="搜索对话"
            value={filter.value}
            onInput={(e: Event) => { filter.value = (e.target as HTMLInputElement).value }}
          />
          {filter.value && (
            <button
              type="button"
              class="sb-search-clear"
              title="清除搜索"
              onClick={() => { filter.value = '' }}
            >
              <X size={12} />
            </button>
          )}
        </div>
        <div class="sb-scroll">
          <div class="sb-section">
            <div class="sb-section-head">
              <span class="sb-section-label">
                对话 {props.sessions.length > 0 ? `(${props.sessions.length})` : ''}
              </span>
              {filter.value.trim() && shown.value.length > 0 ? (
                <button
                  type="button"
                  class="sb-clean-btn danger"
                  disabled={cleaning.value}
                  title="一键删除当前搜索出的全部对话"
                  onMousedown={(e: MouseEvent) => e.preventDefault()}
                  onClick={(e: MouseEvent) => void handleCleanFiltered(e)}
                >
                  {cleaning.value ? '清理中…' : `清理匹配 (${shown.value.length})`}
                </button>
              ) : props.sessions.length > 1 ? (
                <button
                  type="button"
                  class="sb-clean-btn"
                  disabled={cleaning.value}
                  title="保留当前对话，一键清空数据库内其余全部历史对话"
                  onMousedown={(e: MouseEvent) => e.preventDefault()}
                  onClick={(e: MouseEvent) => void handleCleanOthers(undefined, e)}
                >
                  {cleaning.value ? '清空中…' : '清空其余'}
                </button>
              ) : null}
            </div>

            {shown.value.length === 0 && (
              <div class="sb-empty">{filter.value ? '没有匹配的对话' : '暂无对话'}</div>
            )}

            {shown.value.map(s => {
              const sid = sessionIdOf(s)
              const active = sid === props.currentId
              const title = sessionTitle(s)
              const isDup = (titleCounts.value.get(title) || 0) > 1
              const shortSuffix = isDup && sid.length > 4 ? ` #${sid.slice(-3)}` : ''
              const lineage = forkLineage(s)
              return (
                <div
                  key={sid}
                  class={['sb-session-item', active && 'active', lineage && 'forked']}
                  onMousedown={(e: MouseEvent) => {
                    if (e.button === 0) e.preventDefault()
                  }}
                  onClick={() => {
                    closeMenu()
                    props.onSelect(sid)
                  }}
                  onContextmenu={(e: MouseEvent) => onContextMenu(e, s)}
                  onDblclick={() => startRename(sid, title)}
                  title={`${title} (${sid})${lineage ? `\n${lineageText(lineage)}` : ''}\n双击重命名，右键查看更多选项`}
                >
                  {lineage
                    ? <GitBranch size={12} class="sb-session-icon" aria-label={lineageText(lineage)} />
                    : <MessageSquare size={12} class="sb-session-icon" />}
                  {editing.sid === sid ? (
                    <input
                      ref={editInput}
                      class="sb-session-edit"
                      value={editing.value}
                      maxlength={80}
                      disabled={editing.busy}
                      aria-label="对话标题"
                      onInput={(e: Event) => { editing.value = (e.target as HTMLInputElement).value }}
                      onKeydown={(e: KeyboardEvent) => {
                        e.stopPropagation()
                        if (e.isComposing || e.keyCode === 229) return
                        if (e.key === 'Enter') { e.preventDefault(); void commitRename() }
                        else if (e.key === 'Escape') { e.preventDefault(); cancelRename() }
                      }}
                      onBlur={() => void commitRename()}
                      onClick={(e: MouseEvent) => e.stopPropagation()}
                      onMousedown={(e: MouseEvent) => e.stopPropagation()}
                      onDblclick={(e: MouseEvent) => e.stopPropagation()}
                    />
                  ) : (
                    <span class="sb-session-title">
                      {title}
                      {shortSuffix && <em class="sb-session-dup-tag">{shortSuffix}</em>}
                    </span>
                  )}
                  {props.badges[sid] && (
                    <span
                      class={['sb-session-badge', props.badges[sid]]}
                      role="status"
                      title={props.badges[sid] === 'awaiting' ? '等待你的批准或回答' : '正在运行'}
                    />
                  )}
                  {props.onRename && editing.sid !== sid && (
                    <button
                      type="button"
                      class="sb-session-act"
                      title="重命名"
                      aria-label="重命名对话"
                      onMousedown={(e: MouseEvent) => e.stopPropagation()}
                      onClick={(e: MouseEvent) => { e.stopPropagation(); startRename(sid, title) }}
                    >
                      <Pencil size={12} />
                    </button>
                  )}
                  {props.onDelete && editing.sid !== sid && (
                    <button
                      type="button"
                      class="sb-session-del"
                      title="删除此对话"
                      onMousedown={(e: MouseEvent) => e.stopPropagation()}
                      onClick={(e: MouseEvent) => handleDeleteOne(e, sid)}
                    >
                      <Trash2 size={12} />
                    </button>
                  )}
                </div>
              )
            })}
          </div>
        </div>

        <nav class="unified-navigation" aria-label="工作空间">
          <span class="unified-navigation-heading">工作空间</span>

          {VIEWS.map(v => { const Icon = v.icon; const active = layout.state.view === v.id; return (
            <button key={v.id} type="button" class={['unified-navigation-item', active && 'active']} onClick={() => openView(v.id)} aria-current={active ? 'page' : undefined} title={v.hint}>
              <Icon size={16} /><span>{v.label}</span>
            </button>
          )})}
          {pinnedPages.value.length > 0 && <div class="generated-pages-navigation"><span class="unified-navigation-heading">我的页面</span>{pinnedPages.value.map(name => <button key={name} type="button" class={['unified-navigation-item',generatedPageName(layout.state.view)===name && 'active']} onClick={() => openView('app:'+name)}><AppWindow size={16}/><span>{name}</span></button>)}</div>}
          <div class="unified-navigation-bottom">
            <button type="button" title="文件与上下文" onClick={() => layout.toggleContext()}><PanelRight size={15} /><span>上下文</span>{props.pending > 0 && <b>{props.pending}</b>}</button>
            <button type="button" onClick={() => openView('settings')}><Settings size={15} /><span>设置</span></button>
          </div>
        </nav>
        {ctxMenu.visible && (
          <div
            class="sb-context-menu"
            style={{ top: `${ctxMenu.y}px`, left: `${ctxMenu.x}px` }}
            onClick={(e: MouseEvent) => e.stopPropagation()}
          >
            {props.onRename && (
              <button
                type="button"
                class="sb-context-item"
                onClick={() => startRename(ctxMenu.sid, ctxMenu.title)}
              >
                ✏️ 重命名
              </button>
            )}
            {props.onFork && (
              <button
                type="button"
                class="sb-context-item"
                disabled={Boolean(props.badges[ctxMenu.sid])}
                title={props.badges[ctxMenu.sid] ? '正在运行或等你批准的对话不能分叉，先处理完它' : '把整条对话复制成一条新对话继续聊，原对话不动'}
                onClick={(e: MouseEvent) => handleFork(ctxMenu.sid, e)}
              >
                🔀 从此处分叉
              </button>
            )}
            <button
              type="button"
              class="sb-context-item"
              onClick={(e: MouseEvent) => void handleExport(ctxMenu.sid, e)}
            >
              📄 导出为 Markdown
            </button>
            <button
              type="button"
              class="sb-context-item danger"
              onClick={(e: MouseEvent) => handleDeleteOne(e, ctxMenu.sid)}
            >
              🗑 删除此对话
            </button>
            <button
              type="button"
              class="sb-context-item"
              onClick={(e: MouseEvent) => void handleCleanDuplicateTitles(ctxMenu.title, e)}
            >
              🧹 批量删除同名对话
            </button>
            <button
              type="button"
              class="sb-context-item"
              onClick={(e: MouseEvent) => void handleCleanOthers(ctxMenu.sid, e)}
            >
              ✨ 仅保留此对话（清空其余）
            </button>
          </div>
        )}
        <SessionExportDialog />
      </aside>
    )
  },
})
