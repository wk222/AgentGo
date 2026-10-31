// Primary navigation and the project workbench have independent state.
import { computed, reactive, watch } from 'vue'
import { LAYOUT_LIMITS, clamp, loadLayout, saveLayout, snapToNearest, type AgentMode, type ContextTab, type WorkbenchType, type LayoutState } from './layoutModel'
export type { AgentMode, ContextTab } from './layoutModel'

const state = reactive<LayoutState>(loadLayout(localStorage, window.innerWidth))
let viewBeforeSettings = state.view === 'settings' ? 'chat' : state.view
if (state.view === 'settings') state.view = viewBeforeSettings
function restoredPages(): string[] {
  try {
    const value=JSON.parse(localStorage.getItem('agentgo.sceneTabs.v1') || '{}')
    if (Array.isArray(value.openTabs)) return value.openTabs.filter((id: unknown): id is string => typeof id === 'string' && ['chat','create','tasks','extensions'].includes(id))
  } catch {}
  return []
}
const sceneTabs = reactive({
  openTabs: Array.from(new Set(['chat', ...restoredPages(), ...(['chat', 'code'].includes(state.view) ? [] : [state.view])])),
  pinnedTabs: ['chat'], splitViewId: null as string | null,
  navHistory: [state.view], navCursor: 0,
})

watch(() => [...sceneTabs.openTabs], tabs => {
  try {localStorage.setItem('agentgo.sceneTabs.v1',JSON.stringify({version:2,openTabs:tabs.filter(x=>x!=='settings'),pinnedTabs:['chat'],splitViewId:null}))} catch {}
})
watch(state, () => saveLayout(localStorage, { ...state, view: state.view === 'settings' ? viewBeforeSettings : state.view }), { deep: true })
watch(() => state.theme, theme => document.documentElement.classList.toggle('dark', theme === 'dark'), { immediate: true })

function setView(id: string, recordHistory = true, _preserveLayout = true) {
  if (!id) return
  // Compatibility for file links and old tool events: open the editor without
  // changing the user's current page or adding a duplicate editor page tab.
  if (id === 'code' || id === 'memory' || id === 'workflow') { openWorkbench(id); return }
  if (id === 'settings' && state.view !== 'settings') viewBeforeSettings = state.view
  if (!sceneTabs.openTabs.includes(id)) sceneTabs.openTabs.push(id)
  state.view = id
  if (recordHistory && sceneTabs.navHistory[sceneTabs.navCursor] !== id) {
    const history = sceneTabs.navHistory.slice(0, sceneTabs.navCursor + 1)
    history.push(id)
    sceneTabs.navHistory = history.slice(-32)
    sceneTabs.navCursor = sceneTabs.navHistory.length - 1
  }
}
function closeWorkbench() {
  state.agent = 'maximized'
}
function openWorkbench(type?: WorkbenchType) {
  if (type) state.workbenchType = type
  // Opening alongside a page never hides that page; maximize is an explicit action.
  if (state.agent === 'maximized') state.agent = 'docked'
}
function toggleWorkbench() { state.agent === 'maximized' ? openWorkbench() : closeWorkbench() }
function setAgent(mode: AgentMode) {
  if (mode === 'maximized') closeWorkbench()
  else { state.agent = mode }
}
function closeSceneTab(id: string) {
  if (id === 'chat' || sceneTabs.pinnedTabs.includes(id)) return
  const index = sceneTabs.openTabs.indexOf(id)
  if (index < 0) return
  sceneTabs.openTabs.splice(index, 1)
  sceneTabs.navHistory = sceneTabs.navHistory.filter(x => x !== id)
  if (!sceneTabs.navHistory.length) sceneTabs.navHistory = ['chat']
  sceneTabs.navCursor = Math.min(sceneTabs.navCursor, sceneTabs.navHistory.length - 1)
  if (state.view === id) setView(sceneTabs.openTabs[Math.max(0, index - 1)] || 'chat', false)
}
function closeSettings() {
  const previous = viewBeforeSettings
  closeSceneTab('settings')
  setView(previous, false)
}
function goScene(delta: number) {
  let next = sceneTabs.navCursor + delta
  while (next >= 0 && next < sceneTabs.navHistory.length) {
    const target = sceneTabs.navHistory[next]
    if (sceneTabs.openTabs.includes(target)) {
      sceneTabs.navCursor = next; setView(target, false); return
    }
    next += delta
  }
}
export function useLayout() {
  return {
    state, sceneTabs, setView, selectScene: setView, openWorkbench, closeWorkbench, toggleWorkbench,
    closeSettings, closeSceneTab,
    togglePinSceneTab: (id: string) => {
      if (id === 'chat') return
      if (!sceneTabs.openTabs.includes(id)) sceneTabs.openTabs.push(id)
      const index = sceneTabs.pinnedTabs.indexOf(id)
      if (index >= 0) sceneTabs.pinnedTabs.splice(index, 1); else sceneTabs.pinnedTabs.push(id)
    },
    // Retained for callers from older extensions; editor is now a separate pane.
    toggleSplitScene: (_id?: string) => openWorkbench(),
    canGoBackScene: computed(() => sceneTabs.navCursor > 0),
    canGoForwardScene: computed(() => sceneTabs.navCursor < sceneTabs.navHistory.length - 1),
    goBackScene: () => goScene(-1), goForwardScene: () => goScene(1),
    setAgent,
    focusAgent: () => { setView('chat'); state.chatCollapsed = false; if (state.agent === 'hidden') state.agent = 'docked' },
    revealAgent: () => { setView('chat'); state.chatCollapsed = false; if (state.agent === 'hidden') state.agent = 'docked' },
    toggleChat: () => {
      if (state.view !== 'chat') { setView('chat'); state.chatCollapsed = false; return }
      if (state.agent === 'hidden') { state.agent = 'docked'; state.chatCollapsed = false; return }
      state.chatCollapsed = !state.chatCollapsed
    },
    cycleAgent: toggleWorkbench,
    toggleSidebar: () => { state.sidebarOpen = !state.sidebarOpen },
    toggleTheme: () => { state.theme = state.theme === 'dark' ? 'light' : 'dark' },
    openContext: (tab: ContextTab = 'workspace') => { state.contextTab = tab; state.contextOpen = true },
    closeContext: () => { state.contextOpen = false },
    toggleContext: () => { state.contextOpen = !state.contextOpen },
    setSidebarWidth: (w: number, commit = false) => {
      const width = clamp(w, LAYOUT_LIMITS.sidebarMin, LAYOUT_LIMITS.sidebarMax)
      state.sidebarWidth = commit ? snapToNearest(width, LAYOUT_LIMITS.sidebarSnap) : width
    },
    setAgentWidth: (w: number, commit = false) => {
      const width = clamp(w, LAYOUT_LIMITS.agentMin, LAYOUT_LIMITS.agentMax)
      state.agentWidth = commit ? snapToNearest(width, LAYOUT_LIMITS.agentSnap) : width
    },
  }
}
