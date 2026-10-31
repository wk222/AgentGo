// 布局状态的纯逻辑（无 Vue 依赖，便于单测）：默认值、解析、旧键迁移、夹取。
// 响应式封装见 ./useLayout.ts。

export const LAYOUT_KEY = 'agentgo.ui.v1'
export const LAYOUT_VERSION = 4

/** Compatibility values for workbench layout: docked = right pane, maximized = closed, hidden = expanded. */
export type AgentMode = 'docked' | 'maximized' | 'hidden'
export type WorkbenchType = 'code' | 'memory' | 'workflow' | 'apps'
export type ThemeMode = 'dark' | 'light'
export type ContextTab = 'workspace' | 'files' | 'xray'

export interface LayoutState {
  version: number
  theme: ThemeMode
  sidebarOpen: boolean
  sidebarWidth: number
  agent: AgentMode
  agentWidth: number
  workbenchType: WorkbenchType
  chatCollapsed: boolean
  /** 主区当前视图 id（见 app/viewRegistry）。 */
  view: string
  /** 右侧"上下文抽屉"。 */
  contextOpen: boolean
  contextTab: ContextTab
}

// 未经真实使用验证的默认值，集中在这里调整。
export const LAYOUT_LIMITS = {
  sidebarMin: 200,
  sidebarMax: 380,
  sidebarSnap: [200, 240, 300, 360],
  agentMin: 320,
  agentMax: 720,
  agentSnap: [340, 420, 540, 680],
  /** 小于该宽度时，首次启动默认收起会话侧栏。 */
  sidebarAutoOpenWidth: 900,
} as const

export const DEFAULT_VIEW = 'chat'

export function defaultLayout(viewportWidth = 1280): LayoutState {
  return {
    version: LAYOUT_VERSION,
    theme: 'dark',
    sidebarOpen: viewportWidth >= LAYOUT_LIMITS.sidebarAutoOpenWidth,
    sidebarWidth: 240,
    agent: 'maximized',
    agentWidth: 640,
    workbenchType: 'code',
    chatCollapsed: false,
    view: DEFAULT_VIEW,
    contextOpen: false,
    contextTab: 'workspace',
  }
}

export function clamp(n: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, n))
}

export function snapToNearest(val: number, points: readonly number[], range = 16): number {
  for (const p of points) if (Math.abs(val - p) <= range) return p
  return val
}

export interface KV {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

// 旧版散落的键。迁移成功后删除；读不到的键直接取默认值。
const LEGACY_KEYS = [
  'agentgo-dark', 'agentgo-sidebar-open', 'agentgo-center-open', 'agentgo-agent-open',
  'agentgo-sidebar-width', 'agentgo-rightpanel-width', 'agentgo-workspace-panel', 'agentgo-projects',
] as const

function num(raw: string | null): number | null {
  if (raw == null || raw === '') return null
  const n = Number(raw)
  return Number.isFinite(n) ? n : null
}

/** 从旧的 9 个键推出新状态。center/agent 两个布尔量映射到三态：只开 agent = 专注对话。 */
export function migrateLegacy(kv: KV, viewportWidth: number): LayoutState {
  const d = defaultLayout(viewportWidth)
  const centerOpen = kv.getItem('agentgo-center-open') !== 'false'
  const agentOpen = kv.getItem('agentgo-agent-open') !== 'false'
  const sidebarPref = kv.getItem('agentgo-sidebar-open')
  const sw = num(kv.getItem('agentgo-sidebar-width'))
  const aw = num(kv.getItem('agentgo-rightpanel-width'))
  return {
    ...d,
    theme: kv.getItem('agentgo-dark') === 'false' ? 'light' : 'dark',
    sidebarOpen: sidebarPref === null ? d.sidebarOpen : sidebarPref === 'true',
    sidebarWidth: sw == null ? d.sidebarWidth : clamp(sw, LAYOUT_LIMITS.sidebarMin, LAYOUT_LIMITS.sidebarMax),
    agent: !agentOpen ? 'hidden' : centerOpen ? 'docked' : 'maximized',
    agentWidth: aw == null ? d.agentWidth : clamp(aw, LAYOUT_LIMITS.agentMin, LAYOUT_LIMITS.agentMax),
    contextOpen: kv.getItem('agentgo-workspace-panel') === 'open',
  }
}

/** 校验并补全解析出的对象；任何字段不合法都回退默认值，不抛错。 */
export function normalizeLayout(raw: unknown, viewportWidth: number): LayoutState {
  const d = defaultLayout(viewportWidth)
  if (!raw || typeof raw !== 'object') return d
  const r = raw as Record<string, unknown>
  const agent = r.agent === 'docked' || r.agent === 'maximized' || r.agent === 'hidden' ? r.agent : d.agent
  return {
    version: LAYOUT_VERSION,
    theme: r.theme === 'light' ? 'light' : 'dark',
    sidebarOpen: typeof r.sidebarOpen === 'boolean' ? r.sidebarOpen : d.sidebarOpen,
    sidebarWidth: typeof r.sidebarWidth === 'number' ? clamp(r.sidebarWidth, LAYOUT_LIMITS.sidebarMin, LAYOUT_LIMITS.sidebarMax) : d.sidebarWidth,
    agent,
    workbenchType: ['code','memory','workflow','apps'].includes(String(r.workbenchType)) ? r.workbenchType as WorkbenchType : r.view === 'memory' || r.view === 'workflow' ? r.view : 'code',
    chatCollapsed: r.chatCollapsed === true,
    agentWidth: typeof r.agentWidth === 'number' ? clamp(r.agentWidth, LAYOUT_LIMITS.agentMin, LAYOUT_LIMITS.agentMax) : d.agentWidth,
    view: typeof r.view === 'string' && r.view && !['code','memory','workflow'].includes(r.view) ? r.view : d.view,
    contextOpen: typeof r.contextOpen === 'boolean' ? r.contextOpen : d.contextOpen,
    contextTab: r.contextTab === 'files' || r.contextTab === 'xray' ? r.contextTab : 'workspace',
  }
}

/** 读取顺序：新键 → 旧键迁移 → 默认值。迁移时顺带删除旧键。 */
export function loadLayout(kv: KV, viewportWidth: number): LayoutState {
  try {
    const raw = kv.getItem(LAYOUT_KEY)
    if (raw) {
      const parsed = JSON.parse(raw)
      const state = normalizeLayout(parsed, viewportWidth)
      if (parsed.version !== LAYOUT_VERSION) {
        state.agent = 'maximized'
        state.contextOpen = false
        state.sidebarOpen = viewportWidth >= LAYOUT_LIMITS.sidebarAutoOpenWidth
        saveLayout(kv, state)
      }
      return state
    }
  } catch { /* 损坏的 JSON 或存储不可读：落到下面的迁移/默认 */ }
  let hasLegacy = false
  let state: LayoutState
  try {
    hasLegacy = LEGACY_KEYS.some(k => kv.getItem(k) !== null)
    state = hasLegacy ? migrateLegacy(kv, viewportWidth) : defaultLayout(viewportWidth)
  } catch {
    return defaultLayout(viewportWidth)
  }
  try {
    kv.setItem(LAYOUT_KEY, JSON.stringify(state))
    if (hasLegacy) for (const k of LEGACY_KEYS) kv.removeItem(k)
  } catch { /* 存储不可用：本次会话仍可用内存状态 */ }
  return state
}

export function saveLayout(kv: KV, state: LayoutState): void {
  try { kv.setItem(LAYOUT_KEY, JSON.stringify(state)) } catch { /* 偏好可丢 */ }
}

/** Shared pane visibility; collapsing the middle pane also works at narrow widths. */
export function paneVisibility(state: LayoutState, viewportWidth: number) {
  const main = state.agent !== 'hidden' && !(state.view === 'chat' && state.chatCollapsed)
  const workbench = state.agent !== 'maximized'
  const available = viewportWidth - (state.sidebarOpen && viewportWidth >= 900 ? state.sidebarWidth + 5 : 0)
  return {main, workbench, overlay:main && workbench && available < 900,
    rail:!main, empty:!main && !workbench}
}
