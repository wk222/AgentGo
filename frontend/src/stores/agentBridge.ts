// 视图 ↔ Agent 桥接与主舞台（Stage）自动投射状态。
import { reactive, ref } from 'vue'
import { boundedObject, type WorkbenchObject } from './workbenchContext'
import { useLayout } from './useLayout'

export interface AttachedChatFile {
  path: string
  name: string
  isDir?: boolean
}

export const attachedWorkbenchObjects = ref<WorkbenchObject[]>([])
export function attachWorkbenchObject(object: WorkbenchObject) {
  const next=boundedObject(object)
  if (!next.id) return
  const others=attachedWorkbenchObjects.value.filter(x=>x.kind!==next.kind || x.id!==next.id || x.scope!==next.scope)
  attachedWorkbenchObjects.value=[...others.slice(-7),next]
}
export function clearWorkbenchObjects(){attachedWorkbenchObjects.value=[]}

export const attachedChatFiles = ref<AttachedChatFile[]>([])

export function attachFileToChat(rawPath: string, isDir = false) {
  const normalized = String(rawPath || '').trim().replace(/\\/g, '/').replace(/^@+/, '')
  if (!normalized) return
  if (attachedChatFiles.value.some(f => f.path === normalized)) return
  const parts = normalized.split('/').filter(Boolean)
  const name = parts[parts.length - 1] || normalized
  attachedChatFiles.value = [...attachedChatFiles.value, { path: normalized, name, isDir }]
}

export function removeAttachedChatFile(path: string) {
  attachedChatFiles.value = attachedChatFiles.value.filter(f => f.path !== path)
}

export function clearAttachedChatFiles() {
  attachedChatFiles.value = []
}

type Sender = (text: string) => void | Promise<void>

let sender: Sender | null = null

export function registerAgentSender(fn: Sender | null) {
  sender = fn
}

export function sendToAgent(text: string) {
  if (!sender) {
    console.warn('[AgentGo] sendToAgent: 没有注册 Agent 发送器，已丢弃')
    return
  }
  void sender(text)
}

export const stageState = reactive({
  activeAppName: '',
  activeAppVersion: 0,
  activeFilePath: '',
  activeFileDirty: false,
  pendingEditorCommand: '' as '' | 'file' | 'command',
  workflowSelection: null as WorkbenchObject | null,
  memorySelection: null as WorkbenchObject | null,
})

const INNER_APP_TOOLS = new Set([
  'scaffold_inner_app',
  'build_inner_app_iteratively',
  'invoke_inner_app',
  'verify_inner_app',
])

const CODE_STAGE_TOOLS = /^(write_file|edit_file|replace_in_file|str_replace|apply_patch|create_file)/i
const WORKFLOW_STAGE_TOOLS = /^(workflow_run|run_workflow|save_workflow)/i

/**
 * 当 Agent 调用或完成 Inner App / 代码编辑 / 工作流相关工具时，自动将右侧工作台画布切到对应舞台并触发热刷新。
 */
export function projectToolToStage(toolName?: string, rawArgs?: string, isResult = false) {
  const name = String(toolName || '').trim()
  if (!name) return

  if (INNER_APP_TOOLS.has(name)) {
    let appName = ''
    if (rawArgs) {
      try {
        const parsed = JSON.parse(rawArgs)
        appName = String(parsed?.name || parsed?.app_name || '').trim().replace(/^app:/, '')
      } catch {}
    }
    if (appName) {
      stageState.activeAppName = appName
    }
    if (isResult) {
      stageState.activeAppVersion += 1
    }
    if (stageState.activeAppName) {
      useLayout().openWorkbench('apps')
    }
    return
  }

  if (CODE_STAGE_TOOLS.test(name)) {
    if (rawArgs) {
      try {
        const parsed = JSON.parse(rawArgs)
        const p = String(parsed?.path || parsed?.file_path || parsed?.filepath || '').trim()
        if (p) stageState.activeFilePath = p
      } catch {}
    }
    useLayout().setView('code')
    return
  }

  if (WORKFLOW_STAGE_TOOLS.test(name)) {
    useLayout().setView('workflow')
  }
}
