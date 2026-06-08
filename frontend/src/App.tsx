import { defineComponent, ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useChat, sessionIdOf, sessionTitle, type Session } from './composables/useChat'
import { wailsCall } from './wails'
import ChatPanel from './ChatPanel'
import RightPanel from './RightPanel'
import InnerAppHost from './InnerAppHost'
import MemoryPanel from './panels/MemoryPanel'
import { WorkflowPanel, TasksPanel, CapabilityPanel, SkillsPanel, ChannelsPanel, AppsPanel } from './panels'

type FileCat = 'data' | 'code' | 'doc' | 'image'
interface ProjectFile { name: string; cat: FileCat; size: string; updated: string }
interface Project { id: string; name: string; color: string; storage: string; files: ProjectFile[] }

const PROJECT_COLORS = ['#C9A84C', '#5B8FA8', '#8B6BA8', '#4CAF7D', '#E05C5C', '#7B68EE', '#20B2AA']
const DEFAULT_PROJECTS: Project[] = [
  {
    id: 'analytics', name: '数据分析平台', color: '#C9A84C', storage: '—',
    files: [],
  },
]

function loadProjects(): Project[] {
  try {
    const raw = localStorage.getItem('agentgo-projects')
    if (raw) {
      const parsed = JSON.parse(raw)
      if (Array.isArray(parsed) && parsed.length) return parsed
    }
  } catch {}
  return DEFAULT_PROJECTS
}

function saveProjects(list: Project[]) {
  try { localStorage.setItem('agentgo-projects', JSON.stringify(list)) } catch {}
}

const WORKSPACE_NAV = [
  { id: 'apps', label: '内置应用' },
  { id: 'workflow', label: 'Workflow' },
  { id: 'tasks', label: '任务管理' },
  { id: 'capabilities', label: '能力中心' },
]

const KNOWLEDGE_NAV = [
  { id: 'skills', label: 'Skills' },
  { id: 'memory', label: 'Memory' },
  { id: 'channels', label: 'Channels' },
]

const PANEL_ICONS: Record<string, string> = {
  apps: `<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/></svg>`,
  workflow: `<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="5" cy="6" r="2"/><circle cx="19" cy="6" r="2"/><circle cx="12" cy="18" r="2"/><path d="M7 6h10M5 8v8a1 1 0 0 0 .5.87L12 20M19 8v8a1 1 0 0 1-.5.87L12 20"/></svg>`,
  tasks: `<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>`,
  capabilities: `<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l7 4v5c0 4.5-3 7.5-7 9-4-1.5-7-4.5-7-9V7l7-4z"/><path d="M9 12l2 2 4-5"/></svg>`,
  skills: `<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>`,
  memory: `<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9.5 2A2.5 2.5 0 0 1 12 4.5v15a2.5 2.5 0 0 1-4.96-.44 2.5 2.5 0 0 1-2.96-3.08 3 3 0 0 1-.34-5.58 2.5 2.5 0 0 1 1.32-4.24 2.5 2.5 0 0 1 1.98-3A2.5 2.5 0 0 1 9.5 2z"/><path d="M14.5 2A2.5 2.5 0 0 0 12 4.5v15a2.5 2.5 0 0 0 4.96-.44 2.5 2.5 0 0 0 2.96-3.08 3 3 0 0 0 .34-5.58 2.5 2.5 0 0 0-1.32-4.24 2.5 2.5 0 0 0-1.98-3A2.5 2.5 0 0 0 14.5 2z"/></svg>`,
  channels: `<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/><path d="M2 12h20"/></svg>`,
  settings: `<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>`,
}

const VIEW_LABELS: Record<string, string> = {
  apps: '内置应用', workflow: 'Workflow', tasks: '任务管理', capabilities: '能力中心',
  skills: 'Skills', memory: 'Memory', channels: 'Channels', settings: '设置',
}

export default defineComponent({
  name: 'AgentGoApp',
  setup() {
    const windowParams = new URLSearchParams(window.location.search)
    const windowView = windowParams.get('view') || (windowParams.has('workflow') ? 'workflow' : windowParams.has('innerapp') ? 'innerapp' : '')
    if (windowView === 'workflow') {
      const workflowId = windowParams.get('workflow_id') || windowParams.get('workflow') || ''
      return () => (
        <div class="standalone-window standalone-window--workflow">
          <WorkflowPanel initialWorkflowId={workflowId} standalone={true} />
        </div>
      )
    }
    if (windowView === 'innerapp') {
      const appName = windowParams.get('app') || windowParams.get('innerapp') || ''
      return () => (
        <div class="standalone-window standalone-window--innerapp">
          <div class="standalone-window-head">
            <span>{appName || 'Inner App'}</span>
          </div>
          <div class="standalone-window-body">
            <InnerAppHost appName={appName} />
          </div>
        </div>
      )
    }

    const chat = useChat()

    const isDark = ref(localStorage.getItem('agentgo-dark') !== 'false')
    const workspacePanelOpen = ref(localStorage.getItem('agentgo-workspace-panel') !== 'closed')
    const sidebarWidth = ref(Number(localStorage.getItem('agentgo-sidebar-width') || 260))
    const rightPanelWidth = ref(Number(localStorage.getItem('agentgo-rightpanel-width') || 300))
    const resizingPane = ref<'sidebar' | 'right' | ''>('')
    const workflowPopout = ref(false)
    const rightPanelTab = ref<'workspace' | 'files'>('workspace')
    const activeView = ref('chat')

    const projects = ref<Project[]>(loadProjects())
    const activeProjectId = ref(projects.value[0]?.id ?? 'default')
    const projectMenuOpen = ref(false)
    const activeProject = computed(() => projects.value.find(p => p.id === activeProjectId.value) ?? projects.value[0])
    const showNewProject = ref(false)
    const newProjectName = ref('')

    const addProject = () => {
      const name = newProjectName.value.trim()
      if (!name) return
      const id = 'proj-' + Date.now()
      const color = PROJECT_COLORS[projects.value.length % PROJECT_COLORS.length]
      const proj: Project = { id, name, color, storage: '—', files: [] }
      projects.value.push(proj)
      saveProjects(projects.value)
      activeProjectId.value = id
      showNewProject.value = false
      newProjectName.value = ''
      projectMenuOpen.value = false
    }

    const deleteProject = (id: string) => {
      if (projects.value.length <= 1) return
      projects.value = projects.value.filter(p => p.id !== id)
      saveProjects(projects.value)
      if (activeProjectId.value === id) activeProjectId.value = projects.value[0].id
    }

    const sessionFilter = ref('')
    const filteredSessions = computed(() => {
      const q = sessionFilter.value.toLowerCase().trim()
      const all = chat.sessions.value
      if (!q) return all.slice(0, 40)
      return all.filter(s =>
        sessionTitle(s).toLowerCase().includes(q) || sessionIdOf(s).includes(q)
      ).slice(0, 40)
    })

    const llmForm = ref({ api_base: 'https://api.openai.com/v1', api_key: '', model: 'gpt-4o', fallback_model: '' })
    const apiStatus = ref<{ label: string; ok: boolean | null }>({ label: '未检测', ok: null })
    const testing = ref(false)
    const testResult = ref<{ ok: boolean; message?: string } | null>(null)
    const modeProfile = ref('assistant')
    const modeCanvas = ref('balanced')
    const modeSaving = ref(false)

    const applyTheme = () => {
      document.documentElement.classList.toggle('dark', isDark.value)
      localStorage.setItem('agentgo-dark', String(isDark.value))
    }

    const toggleDark = () => { isDark.value = !isDark.value; applyTheme() }

    const openRightPanel = (tab: 'workspace' | 'files' = 'workspace') => {
      rightPanelTab.value = tab
      workspacePanelOpen.value = true
      localStorage.setItem('agentgo-workspace-panel', 'open')
    }

    const toggleWorkspace = () => {
      if (workspacePanelOpen.value) {
        workspacePanelOpen.value = false
        localStorage.setItem('agentgo-workspace-panel', 'closed')
      } else {
        openRightPanel('workspace')
      }
    }

    const clamp = (n: number, min: number, max: number) => Math.max(min, Math.min(max, n))

    const startResize = (pane: 'sidebar' | 'right', e: PointerEvent) => {
      e.preventDefault()
      resizingPane.value = pane
      document.body.classList.add('pane-resizing')
    }

    const handleResize = (e: PointerEvent) => {
      if (resizingPane.value === 'sidebar') {
        sidebarWidth.value = clamp(e.clientX, 200, 380)
      } else if (resizingPane.value === 'right') {
        rightPanelWidth.value = clamp(window.innerWidth - e.clientX, 240, 560)
      }
    }

    const stopResize = () => {
      if (!resizingPane.value) return
      localStorage.setItem('agentgo-sidebar-width', String(sidebarWidth.value))
      localStorage.setItem('agentgo-rightpanel-width', String(rightPanelWidth.value))
      resizingPane.value = ''
      document.body.classList.remove('pane-resizing')
    }

    const switchView = (id: string) => {
      activeView.value = id
      if (id === 'settings') loadLLM()
    }

    const loadLLM = async () => {
      try {
        const cfg = await wailsCall('GetLLMConfig')
        if (cfg) {
          llmForm.value.api_base = cfg.api_base || llmForm.value.api_base
          llmForm.value.model = cfg.model || llmForm.value.model
          llmForm.value.fallback_model = cfg.fallback_model || ''
          apiStatus.value = { label: cfg.api_key_set ? 'API 已配置' : '未配置 Key', ok: !!cfg.api_key_set }
        }
      } catch { apiStatus.value = { label: '浏览器预览', ok: null } }
    }

    const saveLLM = async () => {
      try {
        const r = await wailsCall('SetLLMConfig', llmForm.value.api_base, llmForm.value.api_key, llmForm.value.model, llmForm.value.fallback_model || '')
        if (r?.success) await loadLLM()
      } catch (e: any) { console.error('[AgentGo] saveLLM:', e) }
    }

    const testAPI = async () => {
      testing.value = true; testResult.value = null
      try {
        await saveLLM()
        testResult.value = await wailsCall('TestLLM')
        apiStatus.value = { label: testResult.value?.ok ? 'API 可用' : 'API 异常', ok: testResult.value?.ok ?? false }
      } catch (e: any) { testResult.value = { ok: false, message: e.message } }
      testing.value = false
    }

    const applyModeResponse = (r: any) => {
      if (!r) return
      modeProfile.value = r.profile || modeProfile.value
      modeCanvas.value = r.canvas || modeCanvas.value
    }

    const loadSessionMode = async () => {
      try {
        const sid = chat.sessionId.value
        if (sid) applyModeResponse(await wailsCall('GetSessionModeForSession', sid))
        else {
          const flags = await wailsCall('GetFeatureFlags')
          modeProfile.value = flags?.mode_profile || modeProfile.value
          modeCanvas.value = flags?.execution_canvas || modeCanvas.value
        }
      } catch {}
    }

    const changeSessionMode = async (profile: string, canvas: string) => {
      modeProfile.value = profile
      modeCanvas.value = canvas
      modeSaving.value = true
      try {
        const sid = chat.sessionId.value
        const r = sid
          ? await wailsCall('SetSessionModeForSession', sid, profile, canvas)
          : await wailsCall('SetSessionMode', profile, canvas)
        applyModeResponse(r)
      } catch (e: any) {
        console.error('[AgentGo] changeSessionMode:', e?.message || e)
      } finally {
        modeSaving.value = false
      }
    }

    const sendToAgent = (text: string) => {
      activeView.value = 'chat'
      void chat.sendMessage(text)
    }

    const openWorkflowEditor = async () => {
      try {
        const r = await wailsCall('OpenWorkflowWindow', '')
        if (r?.success) return
      } catch {}
      workflowPopout.value = true
    }

    onMounted(async () => {
      applyTheme()
      window.addEventListener('pointermove', handleResize)
      window.addEventListener('pointerup', stopResize)
      await chat.setupWailsEvents()
      await chat.loadSessions()
      const lastSid = localStorage.getItem('agentgo-last-session')
      if (lastSid && chat.sessions.value.some(s => sessionIdOf(s) === lastSid)) {
        await chat.selectSession(lastSid)
      } else if (chat.sessions.value.length > 0) {
        await chat.selectSession(sessionIdOf(chat.sessions.value[0]))
      } else {
        await chat.newSession()
      }
      await loadSessionMode()
    })

    watch(chat.sessionId, () => { void loadSessionMode() })

    onUnmounted(() => {
      chat.cleanup()
      window.removeEventListener('pointermove', handleResize)
      window.removeEventListener('pointerup', stopResize)
      document.body.classList.remove('pane-resizing')
    })

    /* ── Sidebar render helper ── */
    const Sidebar = () => (
      <aside class="sb">
        {/* Brand */}
        <div class="sb-brand">
          <div class="sb-logo-mark">AG</div>
          <span class="sb-logo-text">AgentGo</span>
          <div class="sb-brand-actions">
            <button class="sb-icon-btn" onClick={toggleDark} title={isDark.value ? '浅色' : '深色'}>
              {isDark.value
                ? <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="5"/><path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42"/></svg>
                : <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
              }
            </button>
          </div>
        </div>

        {/* Project Switcher */}
        <div class="sb-proj-wrap">
          <button class="sb-proj-btn" onClick={() => projectMenuOpen.value = !projectMenuOpen.value}>
            <span class="sb-proj-dot" style={{ background: activeProject.value.color }}></span>
            <span class="sb-proj-name">{activeProject.value.name}</span>
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"/></svg>
          </button>
          {projectMenuOpen.value && (
            <div class="sb-proj-menu">
              {projects.value.map((p: Project) => (
                <button
                  key={p.id}
                  class={['sb-proj-item', p.id === activeProjectId.value && 'active']}
                  onClick={() => { activeProjectId.value = p.id; projectMenuOpen.value = false }}
                >
                  <span class="sb-proj-dot sm" style={{ background: p.color }}></span>
                  <span style={{ flex: 1, textAlign: 'left' as const }}>{p.name}</span>
                  {p.id === activeProjectId.value && (
                    <svg class="sb-proj-check" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
                  )}
                  {projects.value.length > 1 && (
                    <span
                      onClick={(e: MouseEvent) => { e.stopPropagation(); deleteProject(p.id) }}
                      title="删除"
                      style={{ marginLeft: '4px', opacity: 0.4, cursor: 'pointer', fontSize: '13px', lineHeight: 1 }}
                    >✕</span>
                  )}
                </button>
              ))}
              <div style={{ borderTop: '1px solid var(--border-subtle)', margin: '4px 0' }}></div>
              {showNewProject.value ? (
                <div style={{ padding: '4px 8px', display: 'flex', gap: '4px' }}>
                  <input
                    value={newProjectName.value}
                    onInput={(e: Event) => newProjectName.value = (e.target as HTMLInputElement).value}
                    onKeydown={(e: KeyboardEvent) => { if (e.key === 'Enter') addProject(); if (e.key === 'Escape') showNewProject.value = false }}
                    placeholder="项目名称…"
                    style={{ flex: 1, padding: '3px 8px', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', fontSize: '12px', background: 'var(--input-bg)', color: 'var(--text)', outline: 'none' }}
                    autofocus
                  />
                  <button onClick={addProject} style={{ padding: '3px 8px', background: 'var(--accent-bg)', color: 'var(--accent-text)', border: 'none', borderRadius: 'var(--radius-md)', fontSize: '12px', cursor: 'pointer' }}>+</button>
                </div>
              ) : (
                <button
                  class="sb-proj-item"
                  onClick={() => { showNewProject.value = true }}
                  style={{ color: 'var(--text-dim)', fontSize: '12px' }}
                >
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
                  <span>新建项目</span>
                </button>
              )}
            </div>
          )}
        </div>

        {/* New Chat */}
        <button class="sb-new-chat" onClick={chat.newSession}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
          <span>新对话</span>
        </button>

        {/* Search */}
        <div class="sb-search-wrap">
          <svg class="sb-search-icon" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
          <input
            class="sb-search"
            placeholder="搜索对话…"
            value={sessionFilter.value}
            onInput={(e: Event) => sessionFilter.value = (e.target as HTMLInputElement).value}
          />
        </div>

        {/* Scrollable content */}
        <div class="sb-scroll">
          {/* Recent sessions */}
          <div class="sb-section">
            <span class="sb-section-label">近期对话</span>
            {filteredSessions.value.length === 0 && <div class="sb-empty">暂无对话</div>}
            {filteredSessions.value.map(s => {
              const sid = sessionIdOf(s)
              return (
                <button
                  key={sid}
                  class={['sb-session-item', sid === chat.sessionId.value && 'active']}
                  onClick={() => { chat.selectSession(sid); activeView.value = 'chat' }}
                >
                  <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
                  <span class="sb-session-title">{sessionTitle(s)}</span>
                </button>
              )
            })}
          </div>

          {/* Workspace nav */}
          <div class="sb-section">
            <span class="sb-section-label">工作区</span>
            {WORKSPACE_NAV.map(item => (
              <button
                key={item.id}
                class={['sb-nav-item', activeView.value === item.id && 'active']}
                onClick={() => switchView(item.id)}
              >
                <span class="sb-nav-icon" innerHTML={PANEL_ICONS[item.id]}></span>
                <span>{item.label}</span>
              </button>
            ))}
          </div>

          {/* Knowledge nav */}
          <div class="sb-section">
            <span class="sb-section-label">知识库</span>
            {KNOWLEDGE_NAV.map(item => (
              <button
                key={item.id}
                class={['sb-nav-item', activeView.value === item.id && 'active']}
                onClick={() => switchView(item.id)}
              >
                <span class="sb-nav-icon" innerHTML={PANEL_ICONS[item.id]}></span>
                <span>{item.label}</span>
              </button>
            ))}
          </div>
        </div>

        {/* Footer */}
        <div class="sb-footer">
          <button
            class={['sb-nav-item', 'sb-files-btn', workspacePanelOpen.value && rightPanelTab.value === 'files' && 'active']}
            onClick={() => openRightPanel('files')}
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
            <span>Context</span>
          </button>
          <div class="sb-user">
            <div class="sb-user-avatar">AG</div>
            <div class="sb-user-info">
              <span class="sb-user-name">AgentGo</span>
              <span class="sb-user-sub">本地运行</span>
            </div>
            <button class="sb-icon-btn" onClick={() => switchView('settings')} title="设置">
              <span innerHTML={PANEL_ICONS.settings}></span>
            </button>
          </div>
        </div>
      </aside>
    )

    /* ── Settings panel ── */
    const SettingsPanel = () => (
      <div class="settings-panel">
        <div class="settings-header">
          <h2>设置</h2>
          <button class="icon-btn" onClick={() => activeView.value = 'chat'}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </div>
        <div class="settings-section">
          <h3>LLM 配置</h3>
          <div class="settings-form">
            {([
              ['API Base URL', 'api_base', 'text', 'https://api.openai.com/v1'],
              ['API Key', 'api_key', 'password', 'sk-…'],
              ['模型', 'model', 'text', 'gpt-4o'],
              ['备用模型（可选）', 'fallback_model', 'text', 'gpt-3.5-turbo'],
            ] as Array<[string, string, string, string]>).map(([label, key, type, ph]) => (
              <div key={key}>
                <label class="settings-label">{label}</label>
                <input
                  class="settings-input"
                  type={type}
                  placeholder={ph}
                  value={(llmForm.value as any)[key]}
                  onInput={(e: Event) => llmForm.value = { ...llmForm.value, [key]: (e.target as HTMLInputElement).value }}
                />
              </div>
            ))}
          </div>
          <div class="settings-actions">
            <button class="settings-btn primary" onClick={saveLLM}>保存配置</button>
            <button class="settings-btn" onClick={testAPI} disabled={testing.value}>
              {testing.value ? '测试中…' : '测试连接'}
            </button>
            {testResult.value && (
              <span class={['settings-test-result', testResult.value.ok ? 'ok' : 'fail']}>
                {testResult.value.ok ? '✓ 连接正常' : '✗ ' + (testResult.value.message || '连接失败')}
              </span>
            )}
          </div>
          <div class={['settings-api-status', apiStatus.value.ok === true ? 'ok' : apiStatus.value.ok === false ? 'fail' : 'neutral']}>
            {apiStatus.value.label}
          </div>
        </div>
      </div>
    )

    /* ── Real panels ── */
    const PANEL_COMPONENTS: Record<string, any> = {
      memory: MemoryPanel,
      workflow: WorkflowPanel,
      tasks: TasksPanel,
      capabilities: CapabilityPanel,
      skills: SkillsPanel,
      channels: ChannelsPanel,
      apps: AppsPanel,
    }
    const ActivePanel = (view: string) => {
      const Comp = PANEL_COMPONENTS[view]
      if (view === 'workflow') return <WorkflowPanel standalone={false} />
      if (view === 'memory') return <MemoryPanel onSendToAgent={sendToAgent} sessionId={chat.sessionId.value} />
      if (Comp) return <Comp onSendToAgent={sendToAgent} />
      return (
        <div class="stub-panel">
          <div class="stub-icon" innerHTML={PANEL_ICONS[view] || ''}></div>
          <h2 class="stub-title">{VIEW_LABELS[view] || view}</h2>
          <p class="stub-desc">即将推出</p>
          <button class="stub-back" onClick={() => activeView.value = 'chat'}>← 返回对话</button>
        </div>
      )
    }

    const WorkflowPopout = () => workflowPopout.value ? (
      <div class="workflow-popout">
        <div class="workflow-popout-head">
          <span>Workflow</span>
          <button class="icon-btn" onClick={() => workflowPopout.value = false} title="关闭">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </div>
        <div class="workflow-popout-body">
          <WorkflowPanel />
        </div>
      </div>
    ) : null

    return () => (
      <div
        class="layout"
        style={{
          '--sb-width': `${sidebarWidth.value}px`,
          '--rp-width': `${rightPanelWidth.value}px`,
        } as any}
      >
        <Sidebar />
        <div
          class={['pane-resizer', 'pane-resizer-left', resizingPane.value === 'sidebar' && 'active']}
          onPointerdown={(e: PointerEvent) => startResize('sidebar', e)}
          title="拖动调整侧栏宽度"
        ></div>

        <main class="main">
          {activeView.value === 'chat' && (
            <ChatPanel
              messages={chat.messages.value}
              sessionId={chat.sessionId.value}
              sessions={chat.sessions.value}
              sessionLoading={chat.sessionLoading.value}
              sending={chat.sending.value}
              awaitingInteract={chat.awaitingInteract.value}
              runStatusLine={chat.runStatusLine.value}
              chatPaneKey={chat.chatPaneKey.value}
              workspacePanelOpen={workspacePanelOpen.value}
              formInputs={chat.formInputs.value}
              onSend={(text: string) => chat.sendMessage(text)}
              onStop={chat.stopGeneration}
              onToggleWorkspace={toggleWorkspace}
              onReload={chat.reloadCurrentSession}
              onApprove={(id: string) => chat.handleApprove(id)}
              onReject={(id: string) => chat.handleReject(id)}
              onSubmitQuestion={(msg: any, answer: string, idx: number) => chat.submitQuestion(msg, answer, idx)}
              onSubmitAUI={(msg: any, val: string, idx: number) => chat.submitAUIForm(msg, val, idx)}
              onSubmitAUIAction={(msg: any, val: string, idx: number) => chat.submitAUIAction(msg, val, idx)}
              onFormInput={(key: string, field: string, val: any) => {
                chat.formInputs.value[key] = { ...(chat.formInputs.value[key] || {}), [field]: val }
              }}
              modeProfile={modeProfile.value}
              modeCanvas={modeCanvas.value}
              modeSaving={modeSaving.value}
              onModeChange={changeSessionMode}
            />
          )}
          {activeView.value === 'settings' && <SettingsPanel />}
          {activeView.value !== 'chat' && activeView.value !== 'settings' && ActivePanel(activeView.value)}
        </main>

        {workspacePanelOpen.value && (
          <>
            <div
              class={['pane-resizer', 'pane-resizer-right', resizingPane.value === 'right' && 'active']}
              onPointerdown={(e: PointerEvent) => startResize('right', e)}
              title="拖动调整右栏宽度"
            ></div>
            <RightPanel
              activeProject={activeProject.value}
              projects={projects.value}
              onSwitchProject={(id: string) => { activeProjectId.value = id }}
              activeTab={rightPanelTab.value}
              onTabChange={(tab: 'workspace' | 'files') => { rightPanelTab.value = tab }}
              onSendToAgent={sendToAgent}
              onClose={() => {
                workspacePanelOpen.value = false
                localStorage.setItem('agentgo-workspace-panel', 'closed')
              }}
            />
          </>
        )}

        <WorkflowPopout />
        {projectMenuOpen.value && (
          <div class="overlay-bg" onClick={() => projectMenuOpen.value = false}></div>
        )}
      </div>
    )
  },
})
