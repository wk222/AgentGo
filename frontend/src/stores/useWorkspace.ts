// 当前工作区（= 后端 workspace root）。取代旧的 localStorage 假"项目"列表。
// 切换走 SetWorkspaceRoot；version 自增，IDE / 右侧抽屉等据此重新加载。
import { ref } from 'vue'
import { wailsCall } from '../shared/wails'

export interface WorkspaceSummary {
  root: string
  name: string
  branch?: string
  isGit?: boolean
  writable?: boolean
  warning?: string
}

const RECENT_KEY = 'agentgo.recentWorkspaces.v1'
const RECENT_MAX = 8

function loadRecent(): string[] {
  try {
    const raw = JSON.parse(localStorage.getItem(RECENT_KEY) || '[]')
    return Array.isArray(raw) ? raw.filter((p): p is string => typeof p === 'string' && !!p).slice(0, RECENT_MAX) : []
  } catch { return [] }
}

function nameOf(root: string): string {
  const parts = root.replace(/[\\/]+$/, '').split(/[\\/]/)
  return parts[parts.length - 1] || root
}

const info = ref<WorkspaceSummary | null>(null)
const recent = ref<string[]>(loadRecent())
const version = ref(0)
const switching = ref(false)
const error = ref('')
// 切换前的确认钩子（IDE 用它拦截未保存的标签）。任一返回 false 即取消切换，且不算错误。
const guards = new Set<() => boolean | Promise<boolean>>()

function remember(root: string) {
  if (!root) return
  recent.value = [root, ...recent.value.filter(p => p.toLowerCase() !== root.toLowerCase())].slice(0, RECENT_MAX)
  try { localStorage.setItem(RECENT_KEY, JSON.stringify(recent.value)) } catch { /* 偏好可丢 */ }
}

function adopt(raw: any) {
  if (!raw?.root) return
  info.value = {
    root: raw.root,
    name: raw.name || nameOf(raw.root),
    branch: raw.branch,
    isGit: raw.is_git,
    writable: raw.writable,
    warning: raw.warning,
  }
  remember(raw.root)
}

async function refresh() {
  try {
    const raw = await wailsCall('GetWorkspaceInfo')
    if (raw?.success === false) throw new Error(raw.error || '工作区不可用')
    adopt(raw)
    error.value = ''
  } catch (e: any) {
    error.value = e?.message || '工作区信息不可用'
  }
}

async function switchTo(path: string): Promise<boolean> {
  const target = path.trim()
  if (!target || switching.value) return false
  error.value = ''
  for (const g of guards) {
    if (!(await g())) return false
  }
  switching.value = true
  try {
    const raw = await wailsCall('SetWorkspaceRoot', target)
    if (raw?.success === false || raw?.error) throw new Error(raw.error || '切换失败')
    adopt(raw)
    version.value++
    return true
  } catch (e: any) {
    error.value = e?.message || '切换失败'
    return false
  } finally {
    switching.value = false
  }
}

/** IDE / 抽屉内部自己完成了切换：只同步全局信息，不 bump version（它们自己已经重新加载）。 */
function sync(raw: any) {
  adopt(raw)
}

function registerSwitchGuard(fn: () => boolean | Promise<boolean>): () => void {
  guards.add(fn)
  return () => { guards.delete(fn) }
}

export function useWorkspace() {
  return { info, recent, version, switching, error, refresh, switchTo, sync, registerSwitchGuard, nameOf }
}
