import { defineComponent, defineAsyncComponent, computed, onMounted, onUnmounted, watch, reactive } from 'vue'
import { useChat, sessionIdOf, sessionTitle } from '../stores/useChat'
import { wailsCall, wailsCallRaw } from '../shared/wails'
import { loadHarnesses } from '../shared/harness'
import ChatPanel from '../features/chat/ChatPanel'
import { provideChatContext } from '../features/chat/chatContext'
import RightPanel from '../features/context/RightPanel'
import AppStatusBar from './StatusBar'
import { SettingsPanel } from '../features/settings/SettingsPanel'
import UiHost from '../shared/ui/UiHost'
import { notify } from '../shared/ui/notify'
import { Maximize2, Minimize2, X, MessageSquare, Link } from 'lucide-vue-next'
import TitleBar from './TitleBar'
import WorkbenchTabs from './WorkbenchTabs'
import SceneTabBar from './SceneTabBar'
import SessionSidebar from './SessionSidebar'
import { VIEWS, VIEW_BY_ID, DEFAULT_VIEW_ID, WORKBENCH_VIEWS } from './viewRegistry'
import { paneVisibility } from '../stores/layoutModel'
import { useLayout } from '../stores/useLayout'
import { useLlmSettings } from '../stores/useLlmSettings'
import { useSessionMode } from '../stores/useSessionMode'
import { useWorkspace } from '../stores/useWorkspace'
import { useRunningWork } from '../stores/useRunningWork'
import { canJump, mergeBadges } from '../stores/runningWorkModel'
import type { RunningWorkItem } from '../shared/ipc/types'
import { registerAgentSender, stageState, attachFileToChat, attachWorkbenchObject } from '../stores/agentBridge'

import { generatedPageName, loadGeneratedPages, togglePinnedPage } from '../stores/generatedPages'
const GeneratedPageHost = defineAsyncComponent(() => import('../features/extensions/InnerAppHost'))

const COMPACT_WIDTH = 900
const SETTINGS = 'settings'

// 应用壳：只负责"哪个视图在哪个区域"。业务状态在 stores/，视图在 viewRegistry。
export default defineComponent({
  name: 'AppShell',
  setup() {
    const chat = useChat()
    const layout = useLayout()
    const llm = useLlmSettings()
    const mode = useSessionMode()
    const ws = useWorkspace()
    const running = useRunningWork()
    const s = layout.state

    const viewport = reactive({ w: window.innerWidth })
    const compact = computed(() => viewport.w < COMPACT_WIDTH)
    const onResize = () => { viewport.w = window.innerWidth }

    // 已访问过的 keepAlive 视图：懒挂载，之后切走只隐藏不销毁。
    const visited = reactive(new Set<string>())
    const activeViewId = computed(() => (s.view === SETTINGS || generatedPageName(s.view) || VIEW_BY_ID.has(s.view) ? s.view : DEFAULT_VIEW_ID))
    const workbenchVisited = reactive(new Set<string>())
    watch([() => s.agent, () => s.workbenchType], ([mode,type]) => { if (mode !== 'maximized') workbenchVisited.add(type) }, {immediate:true})
    watch(activeViewId, id => {
      if (VIEW_BY_ID.get(id)?.keepAlive) visited.add(id)
    }, { immediate: true })
    const panes = computed(() => paneVisibility(s, viewport.w))
    const workbenchOverlay = computed(() => panes.value.overlay)
    const workbenchWidth = computed(() => {
      const available = viewport.w - (s.sidebarOpen && !compact.value ? s.sidebarWidth + 5 : 0)
      return Math.min(s.agentWidth, Math.max(320, available - 425))
    })

    const currentSession = computed(() => chat.sessions.value.find(x => sessionIdOf(x) === chat.sessionId.value))
    const pending = computed(() => chat.messages.value.filter(m => (m.type === 'approval' || m.type === 'question') && !m.resolved).length)

    const selectedWorkbenchObject = computed(() => {
      if (s.workbenchType === 'code') return stageState.activeFilePath ? {kind:'code',id:stageState.activeFilePath,label:stageState.activeFilePath,dirty:stageState.activeFileDirty} : null
      if (s.workbenchType === 'workflow') return stageState.workflowSelection
      if (s.workbenchType === 'memory') return stageState.memorySelection
      return stageState.activeAppName ? {kind:'apps',id:stageState.activeAppName,label:stageState.activeAppName} : null
    })
    const referenceSelection = () => {
      const object = selectedWorkbenchObject.value
      if (!object) return
      attachWorkbenchObject(object as any)
      layout.revealAgent()
      notify.success('已加入对话，请填写要求后发送')
    }

    const sendChatMessage = async (text: string, images?: string[]) => {
      layout.revealAgent()
      return chat.sendMessage(text, images, async (sessionID) => {
      try {
        const review = await wailsCall('BeginWorkspaceReview', sessionID)
        if (review?.success === false || review?.error) {
          notify.warning(`AI 变更保护未能启动，本次仍可对话，但文件修改不能自动恢复：${review?.error || '未知错误'}`)
        }
      } catch (e: any) {
        notify.warning(`AI 变更保护未能启动，本次仍可对话，但文件修改不能自动恢复：${e?.message || e}`)
      }
      })
    }

    // 拖拽调宽
    let resizing: 'sidebar' | 'agent' | '' = ''
    const startResize = (pane: 'sidebar' | 'agent', e: PointerEvent) => {
      e.preventDefault()
      resizing = pane
      document.body.classList.add('pane-resizing')
    }
    const onMove = (e: PointerEvent) => {
      if (resizing === 'sidebar') {
        layout.setSidebarWidth(e.clientX)
      } else if (resizing === 'agent') {
        layout.setAgentWidth(viewport.w - e.clientX)
      }
    }
    const onUp = () => {
      if (!resizing) return
      if (resizing === 'sidebar') layout.setSidebarWidth(s.sidebarWidth, true)
      else layout.setAgentWidth(s.agentWidth, true)
      resizing = ''
      document.body.classList.remove('pane-resizing')
    }

    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || !(e.ctrlKey || e.metaKey) || e.altKey) return
      const k = e.key.toLowerCase()
      if (k === 'p') {
        e.preventDefault()
        stageState.pendingEditorCommand = e.shiftKey ? 'command' : 'file'
        layout.openWorkbench('code')
        return
      }
      if (e.shiftKey) return
      if (k === 'b') { e.preventDefault(); layout.toggleSidebar() }
      if (k === 'j') { e.preventDefault(); layout.cycleAgent() }
    }

    const newChat = () => { void chat.newSession(); layout.focusAgent(); if (compact.value) s.sidebarOpen = false }
    const selectSession = (id: string) => { void chat.selectSession(id); layout.focusAgent(); if (compact.value) s.sidebarOpen = false }
    // 分叉成功才收起窄屏侧栏、切到对话区；被拒绝（正在运行等）时留在原地，提示已经弹出。
    const onForkFromSidebar = (id: string) => {
      void chat.forkSession(id).then(sid => { if (sid) { layout.focusAgent(); if (compact.value) s.sidebarOpen = false } })
    }

    // 「运行中」：会话列表的圆点以本页自己的运行状态为准，其余（页面刷新前就在跑的、
    // 定时/后台任务）用后端报的补上。本地状态一变就立刻问一次后端，别让它的旧结果多留 2 秒。
    const sidebarBadges = computed(() => mergeBadges(chat.runBadges.value, running.badges.value))
    watch(() => JSON.stringify(chat.runBadges.value), () => { void running.refresh() })
    const jumpToWork = (item: RunningWorkItem) => { if (canJump(item)) selectSession(item.session_id!) }
    const stopWork = async (item: RunningWorkItem) => {
      try {
        if (item.kind === 'task' && item.task_id) {
          const r = await wailsCall('CancelBackgroundTask', item.task_id)
          if (r?.success === false) notify.warning(r.error || '没能取消这个任务')
        } else if (item.kind === 'run' && item.run_id) {
          await wailsCall('CancelEngineRun', item.run_id)
        } else if (item.session_id) {
          await chat.stopSessionById(item.session_id)
        }
      } catch (e: any) {
        notify.error(`停止失败：${e?.message || e}`)
      }
    }

    // ChatPanel 不再收 29 个 props：状态与动作整体注入（见 features/chat/chatContext.ts）。
    provideChatContext({
      messages: chat.messages,
      sessionId: chat.sessionId,
      sessions: chat.sessions,
      sessionLoading: chat.sessionLoading,
      sending: chat.sending,
      forking: chat.forking,
      awaitingInteract: chat.awaitingInteract,
      runStatusLine: chat.runStatusLine,
      chatPaneKey: chat.chatPaneKey,
      contextOpen: computed(() => s.contextOpen),
      formInputs: chat.formInputs,
      modeProfile: mode.profile,
      modeCanvas: mode.canvas,
      modeSaving: mode.saving,
      send: sendChatMessage,
      stop: chat.stopGeneration,
      toggleContext: layout.toggleContext,
      reload: chat.reloadCurrentSession,
      newChat: chat.newSession,
      clearChat: chat.clearSession,
      forkChat: () => { void chat.forkSession() },
      selectSession: id => { void chat.selectSession(id) },
      deleteSession: id => { void chat.deleteSession(id) },
      approve: (id, note, overrideArgs, remember) => { void chat.handleApprove(id, note, overrideArgs, remember) },
      reject: (id, note) => { void chat.handleReject(id, note) },
      steer: text => chat.steerSession(text),
      submitAUI: (msg, val, idx) => { void chat.submitAUIForm(msg, val, idx) },
      submitAUIAction: (msg, val, idx) => { void chat.submitAUIAction(msg, val, idx) },
      submitQuestion: (msg, answer, idx) => { void chat.submitQuestion(msg, answer, idx) },
      setFormInput: (key, field, val) => {
        chat.formInputs.value[key] = { ...(chat.formInputs.value[key] || {}), [field]: val }
      },
      changeMode: (p, c) => { void mode.change(chat.sessionId.value, p, c) },
    })

    watch(chat.sessionId, id => { stageState.memorySelection = null; void mode.load(id) })

    onMounted(async () => {
      window.addEventListener('resize', onResize)
      window.addEventListener('pointermove', onMove)
      window.addEventListener('pointerup', onUp)
      window.addEventListener('keydown', onKey)
      registerAgentSender(text => sendChatMessage(text))
      running.start()

      // 并行启动；任一失败只影响自己，不阻塞壳。
      void loadGeneratedPages()
      void ws.refresh()
      void llm.load()
      void loadHarnesses(wailsCallRaw)
      await chat.setupWailsEvents()
      await chat.loadSessions()
      if (chat.sending.value) return
      const last = localStorage.getItem('agentgo-last-session')
      if (last && chat.sessions.value.some(x => sessionIdOf(x) === last)) await chat.selectSession(last)
      else if (chat.sessions.value.length > 0) await chat.selectSession(sessionIdOf(chat.sessions.value[0]))
      else await chat.newSession()
      await mode.load(chat.sessionId.value)
    })

    onUnmounted(() => {
      registerAgentSender(null)
      running.stop()
      chat.cleanup()
      window.removeEventListener('resize', onResize)
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
      window.removeEventListener('keydown', onKey)
      document.body.classList.remove('pane-resizing')
    })

    const renderMain = () => {
      const active = activeViewId.value
      const ctx = { sessionId: chat.sessionId.value }
      return (
        <main class="primary-page-pane" style={{ display: panes.value.main ? undefined : 'none' }}>
          <SceneTabBar />
          <div id="page-chat" role="tabpanel" aria-label="对话" class="view-slot"
            style={{ display: active === 'chat' ? undefined : 'none' }}><ChatPanel /></div>
          {VIEWS.filter(v => v.keepAlive && visited.has(v.id)).map(v => (
            <div key={v.id} id={'page-' + v.id} role="tabpanel" aria-label={v.label}
              class="view-slot" style={{ display: active === v.id ? undefined : 'none' }}>{v.render(ctx)}</div>
          ))}
          {VIEWS.filter(v => !v.keepAlive && v.id !== 'chat' && v.id === active).map(v => (
            <div key={v.id} id={'page-' + v.id} role="tabpanel" aria-label={v.label} class="view-slot">{v.render(ctx)}</div>
          ))}
          {generatedPageName(active) && (
            <div id={'page-' + active} role="tabpanel" class="view-slot generated-page-slot">
              <header class="generated-page-header"><span>{generatedPageName(active)}</span>
                <button onClick={() => { togglePinnedPage(generatedPageName(active)); layout.focusAgent() }}>取消固定</button>
              </header>
              <GeneratedPageHost appName={generatedPageName(active)} />
            </div>
          )}
          {active === SETTINGS && (
            <div id="page-settings" role="tabpanel" class="view-slot">
              <SettingsPanel theme={s.theme} onToggleTheme={layout.toggleTheme}
                modeProfile={mode.profile.value} executionCanvas={mode.canvas.value}
                onModeChange={(p: string, c: string) => mode.change(chat.sessionId.value, p, c)}
                onSaved={llm.load} onClose={layout.closeSettings} />
            </div>
          )}
        </main>
      )
    }

    const renderWorkbench = () => {
      if (!workbenchVisited.size) return null
      const visible = s.agent !== 'maximized'
      return (
        <>
          {visible && workbenchOverlay.value && <button class="workbench-backdrop" aria-label="收起工作台" onClick={layout.closeWorkbench}/>}
          <aside class={['project-workbench', workbenchOverlay.value && 'is-overlay', (s.agent === 'hidden' || (s.chatCollapsed && activeViewId.value === 'chat')) && !workbenchOverlay.value && 'is-expanded']}
            aria-label="项目工作台" style={{ display: visible ? undefined : 'none', width: workbenchOverlay.value ? undefined : s.agent === 'hidden' || (s.chatCollapsed && activeViewId.value === 'chat') ? undefined : workbenchWidth.value + 'px' }}>
            <header class="workbench-toolbar">
              <WorkbenchTabs />
              <div class="workbench-actions">
                <button type="button" class="workbench-reference-button" aria-label="将选中对象加入对话" title="将选中对象加入对话，不会立即发送"
                  disabled={!selectedWorkbenchObject.value} onClick={referenceSelection}><Link size={14}/></button>
                {!workbenchOverlay.value && <button type="button" aria-label={s.agent === 'hidden' ? '还原工作台' : '放大工作台'}
                  title={s.agent === 'hidden' ? '还原，显示当前页面' : '放大工作台'}
                  onClick={() => layout.setAgent(s.agent === 'hidden' ? 'docked' : 'hidden')}>
                  {s.agent === 'hidden' ? <Minimize2 size={14}/> : <Maximize2 size={14}/>}
                </button>}
                <button type="button" aria-label="收起工作台" title="收起工作台 (Ctrl+J)" onClick={layout.closeWorkbench}><X size={15}/></button>
              </div>
            </header>
            <div class="workbench-content">
              {WORKBENCH_VIEWS.filter(v=>workbenchVisited.has(v.id)).map(v=><div key={v.id} class="workbench-view" style={{display:s.workbenchType===v.id?undefined:'none'}}>{v.render({sessionId:chat.sessionId.value})}</div>)}
            </div>
          </aside>
        </>
      )
    }

    return () => (
      <div class="app-window-root">
        <UiHost />
        <TitleBar
          sidebarOpen={s.sidebarOpen}
          agent={s.agent}
          isDark={s.theme === 'dark'}
          sessionTitle={currentSession.value ? sessionTitle(currentSession.value) : ''}
          settingsActive={activeViewId.value === SETTINGS}
          sending={chat.sending.value}
          onToggleSidebar={layout.toggleSidebar}
          onSetAgent={layout.setAgent}
          onToggleDark={layout.toggleTheme}
          onOpenSettings={() => (activeViewId.value === SETTINGS ? layout.closeSettings() : (layout.setView(SETTINGS), void llm.load()))}
        />

        <div class={['layout', 'app-layout-body', compact.value && 'compact-layout']} style={{ '--sb-width': `${s.sidebarWidth}px`, '--rp-width': `${s.agentWidth}px` } as any}>


          {s.sidebarOpen && compact.value && <button class="navigation-backdrop" aria-label="关闭导航" onClick={() => { s.sidebarOpen = false }} />}
          {s.sidebarOpen && (
            <>
              <SessionSidebar
                pending={pending.value}
                sessions={chat.sessions.value}
                currentId={chat.sessionId.value}
                badges={sidebarBadges.value}
                onNew={newChat}
                onSelect={selectSession}
                onDelete={(id: string) => void chat.deleteSession(id)}
                onRename={(id: string, title: string) => chat.renameSession(id, title)}
                onFork={onForkFromSidebar}
                onDeleteBatch={(ids: string[]) => void chat.deleteSessionsBatch(ids)}
                onDeleteOthers={(keepId?: string) => chat.deleteOtherSessions(keepId)}
                onDeleteByTitle={(title: string) => chat.deleteSessionsByTitle(title)}
              />
              <div class="pane-resizer pane-resizer-left" onPointerdown={(e: PointerEvent) => startResize('sidebar', e)} title="拖动调整侧栏宽度" />
            </>
          )}

          <div class="page-and-workbench-shell">
            <div class="page-and-workbench-body">
              {renderMain()}
              {panes.value.rail && (
                <div class="collapsed-chat-rail">
                  <button type="button" aria-label="展开对话" title="展开对话" onClick={layout.revealAgent}><MessageSquare size={17}/><span>对话</span>{pending.value > 0 && <b>{pending.value}</b>}</button>
                  {chat.sending.value && <span class="collapsed-chat-running" role="status" title={chat.runStatusLine.value || '运行中'}>运行中</span>}
                </div>
              )}
              {s.agent === 'docked' && panes.value.main && !workbenchOverlay.value && (
                <div class="pane-resizer pane-resizer-right" role="separator" aria-orientation="vertical" aria-label="调整工作台宽度"
                  onPointerdown={(e: PointerEvent) => startResize('agent', e)} />
              )}
              {renderWorkbench()}
              {panes.value.empty && <div class="pane-empty-state"><MessageSquare size={24}/><p>对话和工作台已收起</p><div><button type="button" onClick={layout.revealAgent}>展开对话</button><button type="button" onClick={()=>layout.openWorkbench()}>展开工作台</button></div></div>}
            </div>
          </div>

          {s.contextOpen && (
            <>
              <div class="context-drawer-backdrop" onClick={layout.closeContext} />
              <div class="context-drawer-shell">
                <RightPanel
                  activeTab={s.contextTab}
                  onTabChange={(tab: 'workspace' | 'files' | 'xray') => { s.contextTab = tab }}
                  onSendToAgent={(text: string) => void sendChatMessage(text)}
                  onClose={layout.closeContext}
                />
              </div>
            </>
          )}
        </div>

        <AppStatusBar
          model={llm.form.value.model}
          modeProfile={mode.profile.value}
          modeCanvas={mode.canvas.value}
          sending={chat.sending.value}
          sessionId={chat.sessionId.value}
          statusLine={chat.runStatusLine.value}
          onJumpWork={jumpToWork}
          onStopWork={stopWork}
        />
      </div>
    )
  },
})
