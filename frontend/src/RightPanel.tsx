import { defineComponent, ref, computed, onMounted, onUnmounted, watch, PropType } from 'vue'
import {
  AlertTriangle,
  ArrowUp,
  CheckCircle2,
  File,
  FileCode,
  FileJson,
  FileText,
  Folder,
  FolderOpen,
  GitBranch,
  GitCompare,
  Image as ImageIcon,
  Lock,
  MessageSquareText,
  Plus,
  RefreshCw,
  Search,
  Send,
  Settings2,
  ShieldCheck,
  Table2,
  X,
} from 'lucide-vue-next'
import { wailsCall } from './wails'

interface ProjectFile {
  name: string
  cat: 'data' | 'code' | 'doc' | 'image'
  size: string
  updated: string
}

interface Project {
  id: string
  name: string
  color: string
  storage: string
  files: ProjectFile[]
}

interface WorkspaceEntry {
  name: string
  path: string
  is_dir: boolean
  size: number
  mod_time: string
  git_status?: string
}

interface WorkspaceInfo {
  success?: boolean
  root: string
  name: string
  git_root?: string
  branch?: string
  dirty_count?: number
  is_git?: boolean
  writable?: boolean
  warning?: string
  preview_limit_bytes?: number
  watch_mode?: string
}

type ContextFile = { path: string; size: number; content: string; truncated: boolean }

const MAX_PREVIEW_BYTES = 512 * 1024
const MAX_CONTEXT_BYTES = 48 * 1024
const WORKSPACE_POLL_MS = 15000

export default defineComponent({
  name: 'RightPanel',
  props: {
    activeProject: { type: Object as PropType<Project>, required: true },
    projects: { type: Array as PropType<Project[]>, required: true },
    onSwitchProject: { type: Function as PropType<(id: string) => void>, required: true },
    activeTab: { type: String as PropType<'workspace' | 'files'>, default: 'workspace' },
    onTabChange: { type: Function as PropType<(tab: 'workspace' | 'files') => void>, default: undefined },
    onSendToAgent: { type: Function as PropType<(text: string) => void>, default: undefined },
    onClose: { type: Function as PropType<() => void>, default: undefined },
  },
  setup(props) {
    const activeTab = ref<'workspace' | 'files'>(props.activeTab)
    const workspaceInfo = ref<WorkspaceInfo | null>(null)
    const rootEditorOpen = ref(false)
    const rootDraft = ref('')
    const rootSaving = ref(false)
    const rootNotice = ref('')

    const wsPath = ref('')
    const wsEntries = ref<WorkspaceEntry[]>([])
    const wsQuery = ref('')
    const wsLoading = ref(false)
    const wsError = ref('')
    const wsPreviewFile = ref('')
    const wsPreviewContent = ref('')
    const wsPreviewMode = ref<'file' | 'diff'>('file')
    const wsPreviewEntry = ref<WorkspaceEntry | null>(null)
    const wsPreviewLoading = ref(false)
    const lastRefreshAt = ref('')
    const contextFiles = ref<ContextFile[]>([])
    const contextNotice = ref('')

    let pollTimer: number | undefined

    watch(() => props.activeTab, tab => {
      activeTab.value = tab
    })

    const setTab = (tab: 'workspace' | 'files') => {
      activeTab.value = tab
      props.onTabChange?.(tab)
    }

    const clearPreview = () => {
      wsPreviewFile.value = ''
      wsPreviewContent.value = ''
      wsPreviewEntry.value = null
      wsPreviewMode.value = 'file'
    }

    const refreshWorkspaceInfo = async () => {
      try {
        const info = await wailsCall('GetWorkspaceInfo')
        if (info?.success === false) throw new Error(info.error || 'workspace unavailable')
        workspaceInfo.value = info
        if (!rootEditorOpen.value) rootDraft.value = info?.root || ''
      } catch (e: any) {
        if (!workspaceInfo.value) {
          workspaceInfo.value = {
            root: '',
            name: 'Workspace',
            warning: e?.message || 'Workspace 信息不可用',
            writable: false,
          }
        }
      }
    }

    const loadWorkspace = async (rel: string, preservePreview = false) => {
      wsLoading.value = true
      wsError.value = ''
      if (!preservePreview) clearPreview()
      try {
        const entries = (await wailsCall('ListWorkspace', rel)) || []
        wsEntries.value = Array.isArray(entries) ? entries : []
        wsPath.value = rel
        lastRefreshAt.value = new Date().toLocaleTimeString('zh-CN', { hour12: false })
      } catch (e: any) {
        wsError.value = e?.message || '加载失败'
      } finally {
        wsLoading.value = false
      }
    }

    const refreshCurrent = async () => {
      await refreshWorkspaceInfo()
      await loadWorkspace(wsPath.value, true)
      rootNotice.value = '已刷新'
    }

    const applyWorkspaceRoot = async () => {
      const nextRoot = rootDraft.value.trim()
      if (!nextRoot) return
      rootSaving.value = true
      rootNotice.value = ''
      try {
        const info = await wailsCall('SetWorkspaceRoot', nextRoot)
        if (info?.success === false) throw new Error(info.error || '切换失败')
        workspaceInfo.value = info
        rootDraft.value = info.root || nextRoot
        rootEditorOpen.value = false
        contextFiles.value = []
        contextNotice.value = ''
        rootNotice.value = `工作区已切换：${info.root || nextRoot}`
        await loadWorkspace('', false)
      } catch (e: any) {
        rootNotice.value = e?.message || '切换失败'
      } finally {
        rootSaving.value = false
      }
    }

    const openEntry = async (entry: WorkspaceEntry) => {
      if (entry.is_dir) {
        await loadWorkspace(entry.path)
        return
      }
      wsPreviewFile.value = entry.path
      wsPreviewEntry.value = entry
      wsPreviewMode.value = 'file'
      wsPreviewContent.value = ''
      wsPreviewLoading.value = true
      try {
        const previewLimit = workspaceInfo.value?.preview_limit_bytes || MAX_PREVIEW_BYTES
        if (entry.size > previewLimit) {
          wsPreviewContent.value = `文件较大 (${fmtSize(entry.size)})，已限制直接预览。可以查看 diff，或让 Agent 按需读取片段。`
          return
        }
        const r = await wailsCall('ReadWorkspaceFile', entry.path)
        wsPreviewContent.value = r?.success === false
          ? '无法读取文件: ' + (r?.error || '未知错误')
          : (typeof r === 'string' ? r : (r?.content || JSON.stringify(r, null, 2)))
      } catch (e: any) {
        wsPreviewContent.value = '无法读取文件: ' + (e?.message || '未知错误')
      } finally {
        wsPreviewLoading.value = false
      }
    }

    const goUp = async () => {
      if (!wsPath.value) return
      const parts = wsPath.value.replace(/\/$/, '').split('/')
      parts.pop()
      await loadWorkspace(parts.join('/'))
    }

    onMounted(async () => {
      await refreshWorkspaceInfo()
      await loadWorkspace('')
      pollTimer = window.setInterval(() => {
        if (activeTab.value !== 'workspace' || wsLoading.value || rootEditorOpen.value) return
        refreshWorkspaceInfo()
        if (!wsPreviewFile.value) loadWorkspace(wsPath.value, true)
      }, WORKSPACE_POLL_MS)
    })

    onUnmounted(() => {
      if (pollTimer) window.clearInterval(pollTimer)
    })

    const readEntryText = async (entry: WorkspaceEntry) => {
      const previewLimit = workspaceInfo.value?.preview_limit_bytes || MAX_PREVIEW_BYTES
      if (entry.size > previewLimit) {
        throw new Error(`文件较大 (${fmtSize(entry.size)})，已限制直接读取。`)
      }
      if (
        wsPreviewFile.value === entry.path &&
        wsPreviewMode.value === 'file' &&
        wsPreviewContent.value &&
        !wsPreviewContent.value.startsWith('无法读取文件')
      ) {
        return wsPreviewContent.value
      }
      const r = await wailsCall('ReadWorkspaceFile', entry.path)
      if (r?.success === false) throw new Error(r?.error || '读取失败')
      return typeof r === 'string' ? r : String(r?.content || '')
    }

    const addToContext = async (entry?: WorkspaceEntry | null) => {
      const target = entry || wsPreviewEntry.value
      if (!target || target.is_dir) return
      try {
        const content = await readEntryText(target)
        const exists = contextFiles.value.some(x => x.path === target.path)
        const clipped = content.length > MAX_CONTEXT_BYTES
        const item = {
          path: target.path,
          size: target.size || content.length,
          content: clipped ? content.slice(0, MAX_CONTEXT_BYTES) : content,
          truncated: clipped,
        }
        contextFiles.value = exists
          ? contextFiles.value.map(x => x.path === target.path ? item : x)
          : [...contextFiles.value, item]
        contextNotice.value = `已加入上下文：${target.path}`
      } catch (e: any) {
        contextNotice.value = e?.message || '加入上下文失败'
      }
    }

    const explainEntry = async (entry?: WorkspaceEntry | null) => {
      const target = entry || wsPreviewEntry.value
      if (!target || target.is_dir || !props.onSendToAgent) return
      try {
        const content = await readEntryText(target)
        props.onSendToAgent(`请解释这个工作区文件，重点说明它的职责、关键逻辑、可能风险，以及适合怎么改造。\n\n文件：${target.path}\n\n\`\`\`\n${content.slice(0, MAX_CONTEXT_BYTES)}\n\`\`\``)
      } catch (e: any) {
        contextNotice.value = e?.message || '无法发送给 Agent'
      }
    }

    const sendContextToAgent = () => {
      if (!contextFiles.value.length || !props.onSendToAgent) return
      const body = contextFiles.value.map(f =>
        `文件：${f.path}${f.truncated ? '（已截断）' : ''}\n\`\`\`\n${f.content}\n\`\`\``
      ).join('\n\n')
      props.onSendToAgent(`请基于以下 Workspace 上下文继续分析，并给出下一步建议。\n\n${body}`)
    }

    const viewDiff = async (entry?: WorkspaceEntry | null) => {
      const target = entry || wsPreviewEntry.value
      if (!target || target.is_dir) return
      wsPreviewFile.value = target.path
      wsPreviewEntry.value = target
      wsPreviewMode.value = 'diff'
      wsPreviewLoading.value = true
      try {
        const r = await wailsCall('WorkspaceFileDiff', target.path)
        if (r?.success === false) {
          wsPreviewContent.value = '无法查看 diff: ' + (r?.error || '未知错误')
        } else if (r?.has_changes) {
          wsPreviewContent.value = String(r.diff || (r.untracked ? '未跟踪文件，暂无 HEAD diff。' : ''))
        } else {
          wsPreviewContent.value = '相对 HEAD 暂无改动。'
        }
      } catch (e: any) {
        wsPreviewContent.value = '无法查看 diff: ' + (e?.message || '未知错误')
      } finally {
        wsPreviewLoading.value = false
      }
    }

    const removeContext = (path: string) => {
      contextFiles.value = contextFiles.value.filter(f => f.path !== path)
    }

    const copyPath = async (path: string) => {
      try {
        await navigator.clipboard?.writeText(path)
        contextNotice.value = `已复制路径：${path}`
      } catch {
        contextNotice.value = path
      }
    }

    const visibleEntries = computed(() => {
      const q = wsQuery.value.trim().toLowerCase()
      if (!q) return wsEntries.value
      return wsEntries.value.filter(e => e.name.toLowerCase().includes(q) || e.path.toLowerCase().includes(q))
    })

    const workspaceParts = computed(() => wsPath.value.split('/').filter(Boolean))
    const contextBytes = computed(() => contextFiles.value.reduce((sum, f) => sum + (f.size || f.content.length), 0))

    const fmtSize = (bytes: number) => {
      if (!bytes) return '0 B'
      if (bytes < 1024) return bytes + ' B'
      if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
      return (bytes / 1024 / 1024).toFixed(1) + ' MB'
    }

    const fileIcon = (entry: WorkspaceEntry) => {
      if (entry.is_dir) return <Folder size={15} />
      const ext = entry.name.split('.').pop()?.toLowerCase() || ''
      if (['go', 'ts', 'tsx', 'js', 'jsx', 'py', 'rs', 'java', 'cpp', 'c', 'sh'].includes(ext)) return <FileCode size={15} />
      if (['json', 'yaml', 'yml', 'toml'].includes(ext)) return <FileJson size={15} />
      if (['csv', 'xlsx', 'xls'].includes(ext)) return <Table2 size={15} />
      if (['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp'].includes(ext)) return <ImageIcon size={15} />
      if (['md', 'txt', 'pdf', 'doc', 'docx', 'tex'].includes(ext)) return <FileText size={15} />
      return <File size={15} />
    }

    const statusLabel = (status?: string) => {
      if (!status) return ''
      if (status === 'dirty') return 'M'
      if (status === '??') return 'U'
      return status.replace(/\s/g, '').slice(0, 2)
    }

    const ActionButton = (label: string, icon: any, onClick: (e: MouseEvent) => void, disabled = false) => (
      <button
        class="rp-action-btn"
        disabled={disabled}
        onClick={(e: MouseEvent) => { e.stopPropagation(); onClick(e) }}
      >
        {icon}
        <span>{label}</span>
      </button>
    )

    const IconButton = (title: string, icon: any, onClick: (e: MouseEvent) => void, disabled = false) => (
      <button
        class="rp-icon-btn"
        title={title}
        disabled={disabled}
        onClick={(e: MouseEvent) => { e.stopPropagation(); onClick(e) }}
      >
        {icon}
      </button>
    )

    const renderRootHeader = () => {
      const info = workspaceInfo.value
      return (
        <div class="rp-workspace-head">
          <div class="rp-root-main">
            <div class="rp-root-icon"><FolderOpen size={16} /></div>
            <div class="rp-root-text">
              <div class="rp-root-title">
                <span>{info?.name || 'Workspace'}</span>
                <span class="rp-root-lock" title="文件访问限制在当前 workspace root 内"><Lock size={11} />Root</span>
              </div>
              <div class="rp-root-path" title={info?.root || ''}>{info?.root || '未设置工作区'}</div>
            </div>
          </div>

          <div class="rp-root-meta">
            {info?.is_git && (
              <span><GitBranch size={12} />{info.branch || 'git'}</span>
            )}
            {info?.is_git && (
              <span class={info.dirty_count ? 'warn' : ''}>{info.dirty_count || 0} changes</span>
            )}
            <span class={info?.writable ? 'ok' : 'warn'}>
              {info?.writable ? <CheckCircle2 size={12} /> : <AlertTriangle size={12} />}
              {info?.writable ? 'writable' : 'read-only'}
            </span>
            <span>{info?.watch_mode || 'poll'} refresh</span>
          </div>

          {info?.warning && (
            <div class="rp-root-warning"><AlertTriangle size={13} />{info.warning}</div>
          )}
          {rootNotice.value && <div class="rp-root-notice">{rootNotice.value}</div>}

          {rootEditorOpen.value && (
            <div class="rp-root-editor">
              <input
                value={rootDraft.value}
                placeholder="C:\\Users\\wgk\\Documents\\GitHub\\agentgo"
                onInput={(e: Event) => rootDraft.value = (e.target as HTMLInputElement).value}
                onKeydown={(e: KeyboardEvent) => { if (e.key === 'Enter') applyWorkspaceRoot() }}
              />
              <button onClick={applyWorkspaceRoot} disabled={rootSaving.value}>{rootSaving.value ? '应用中' : '应用'}</button>
              <button onClick={() => { rootEditorOpen.value = false; rootDraft.value = workspaceInfo.value?.root || '' }}>取消</button>
            </div>
          )}

          <div class="rp-toolbar">
            <label class="rp-searchbox">
              <Search size={13} />
              <input
                value={wsQuery.value}
                placeholder="搜索当前目录"
                onInput={(e: Event) => wsQuery.value = (e.target as HTMLInputElement).value}
              />
            </label>
            {IconButton('刷新', <RefreshCw size={14} />, () => refreshCurrent(), wsLoading.value)}
            {IconButton('设置工作区目录', <Settings2 size={14} />, () => { rootEditorOpen.value = !rootEditorOpen.value; rootDraft.value = workspaceInfo.value?.root || '' })}
          </div>
        </div>
      )
    }

    const renderBreadcrumb = () => (
      <div class="rp-breadcrumb">
        <button onClick={() => loadWorkspace('')}>~/</button>
        {workspaceParts.value.map((part, i, arr) => (
          <>
            <span>/</span>
            <button
              class={i === arr.length - 1 ? 'active' : ''}
              onClick={() => loadWorkspace(arr.slice(0, i + 1).join('/'))}
            >
              {part}
            </button>
          </>
        ))}
        {wsPath.value && (
          <button class="rp-up-btn" onClick={goUp} title="上级目录"><ArrowUp size={13} /></button>
        )}
      </div>
    )

    const renderContextStrip = () => contextFiles.value.length > 0 && !wsPreviewFile.value ? (
      <div class="rp-context-strip">
        <div>
          <strong>{contextFiles.value.length}</strong>
          <span>{fmtSize(contextBytes.value)} in context</span>
        </div>
        <button onClick={sendContextToAgent} disabled={!props.onSendToAgent}>
          <Send size={12} />发送
        </button>
      </div>
    ) : null

    const renderPreview = () => wsPreviewFile.value ? (
      <div class="rp-preview">
        <div class="rp-preview-head">
          <span title={wsPreviewFile.value}>{wsPreviewFile.value}</span>
          {IconButton('关闭预览', <X size={14} />, () => clearPreview())}
        </div>
        <div class="rp-preview-actions">
          {ActionButton('加入上下文', <Plus size={13} />, () => addToContext(wsPreviewEntry.value), !wsPreviewEntry.value)}
          {ActionButton('解释', <MessageSquareText size={13} />, () => explainEntry(wsPreviewEntry.value), !props.onSendToAgent || !wsPreviewEntry.value)}
          {ActionButton('Diff', <GitCompare size={13} />, () => viewDiff(wsPreviewEntry.value), !wsPreviewEntry.value)}
          {ActionButton('复制路径', <FileText size={13} />, () => copyPath(wsPreviewFile.value), !wsPreviewFile.value)}
          <span>{wsPreviewMode.value === 'diff' ? 'Diff' : (wsPreviewEntry.value?.size ? fmtSize(wsPreviewEntry.value.size) : '')}</span>
        </div>
        <pre>{wsPreviewLoading.value ? '加载中...' : wsPreviewContent.value}</pre>
      </div>
    ) : null

    const renderEntryList = () => !wsPreviewFile.value ? (
      <div class="rp-entry-list">
        {wsLoading.value && <div class="rp-empty">加载中...</div>}
        {wsError.value && <div class="rp-error">{wsError.value}</div>}
        {!wsLoading.value && !wsError.value && visibleEntries.value.length === 0 && (
          <div class="rp-empty">空目录</div>
        )}
        {visibleEntries.value.map(entry => (
          <div
            key={entry.path}
            class={['rp-entry', entry.is_dir && 'dir']}
            onClick={() => openEntry(entry)}
          >
            <span class="rp-entry-icon">{fileIcon(entry)}</span>
            <div class="rp-entry-main">
              <div class="rp-entry-name">
                <span>{entry.name}</span>
                {entry.git_status && <span class={['rp-git-badge', entry.git_status === '??' && 'new']} title={entry.git_status}>{statusLabel(entry.git_status)}</span>}
              </div>
              {!entry.is_dir && <div class="rp-entry-sub">{fmtSize(entry.size)}</div>}
            </div>
            {!entry.is_dir && (
              <div class="rp-entry-actions">
                {IconButton('加入上下文', <Plus size={13} />, () => addToContext(entry))}
                {IconButton('让 Agent 解释', <MessageSquareText size={13} />, () => explainEntry(entry), !props.onSendToAgent)}
                {IconButton('查看 diff', <GitCompare size={13} />, () => viewDiff(entry))}
              </div>
            )}
          </div>
        ))}
      </div>
    ) : null

    const renderWorkspaceTab = () => (
      <div class="rp-content rp-workspace">
        {renderRootHeader()}
        {renderBreadcrumb()}
        {lastRefreshAt.value && <div class="rp-refresh-line">Last refresh {lastRefreshAt.value}</div>}
        {renderContextStrip()}
        {contextNotice.value && !wsPreviewFile.value && <div class="rp-context-notice">{contextNotice.value}</div>}
        {renderPreview()}
        {renderEntryList()}
      </div>
    )

    const renderContextTab = () => (
      <div class="rp-content rp-context-pack">
        <div class="rp-pack-head">
          <div>
            <div class="rp-pack-title">
              <span style={{ background: props.activeProject.color }}></span>
              {props.activeProject.name}
            </div>
            <div class="rp-pack-sub">
              {contextFiles.value.length} files · {fmtSize(contextBytes.value)} selected
            </div>
          </div>
          <button onClick={sendContextToAgent} disabled={!contextFiles.value.length || !props.onSendToAgent}>
            <Send size={13} />发送
          </button>
        </div>

        <div class="rp-pack-root">
          <ShieldCheck size={14} />
          <span title={workspaceInfo.value?.root || ''}>{workspaceInfo.value?.root || 'Workspace root unavailable'}</span>
        </div>

        <div class="rp-pack-list">
          {contextFiles.value.length === 0 && <div class="rp-empty">暂无上下文文件</div>}
          {contextFiles.value.map(f => (
            <div class="rp-pack-item" key={f.path}>
              <FileText size={14} />
              <div>
                <strong title={f.path}>{f.path}</strong>
                <span>{fmtSize(f.size)}{f.truncated ? ' · clipped' : ''}</span>
              </div>
              <button onClick={() => removeContext(f.path)} title="移除"><X size={13} /></button>
            </div>
          ))}
        </div>
      </div>
    )

    return () => (
      <aside class="rightpanel">
        <div class="rp-tabs">
          <button
            class={['rp-tab', activeTab.value === 'workspace' && 'active']}
            onClick={() => setTab('workspace')}
          >Explorer</button>
          <button
            class={['rp-tab', activeTab.value === 'files' && 'active']}
            onClick={() => setTab('files')}
          >Context {contextFiles.value.length > 0 ? contextFiles.value.length : ''}</button>
          <button class="rp-close" onClick={() => props.onClose?.()} title="隐藏右栏">×</button>
        </div>

        {activeTab.value === 'workspace' && renderWorkspaceTab()}
        {activeTab.value === 'files' && renderContextTab()}
      </aside>
    )
  },
})
