// 视图的唯一登记处：新增一个主区视图 = 在这里加一条，不再改 App 里的 if/else。
// 重型工作台视图（Monaco / VueFlow 等）使用 defineAsyncComponent 异步分包，加速首屏启动。
import { defineAsyncComponent } from 'vue'
import { MessageSquare, Code2, AppWindow, Workflow, CheckSquare, Brain, Boxes, Sparkles } from 'lucide-vue-next'
import { stageState, sendToAgent } from '../stores/agentBridge'

const CodePanel = defineAsyncComponent(() => import('../features/workspace/CodeWorkspacePanel'))
const PreviewPanel = defineAsyncComponent(() => import('../features/extensions/InnerAppHost'))

const WorkflowPanel = defineAsyncComponent(() =>
  import('../features/workflow/WorkflowPanel').then(m => m.WorkflowPanel),
)
const TasksPanel = defineAsyncComponent(() =>
  import('../features/tasks/TasksPanel').then(m => m.TasksPanel),
)
const MemoryPanel = defineAsyncComponent(() => import('../features/memory/MemoryPanel'))
const ExtensionsPanel = defineAsyncComponent(() => import('../features/extensions/ExtensionsPanel'))

const CreationCenter = defineAsyncComponent(() => import('../features/extensions/CreationCenter'))

export interface ViewCtx {
  sessionId: string
}

export interface ViewDef {
  id: string
  label: string
  hint: string
  icon: any
  /** 切走时是否保留（保住 Monaco 标签、终端缓冲、画布缩放）。同时保活的重视图 ≤ 2 个。 */
  keepAlive: boolean
  render: (ctx: ViewCtx) => any
}

export const VIEWS: ViewDef[] = [
  { id: 'chat', label: '对话', hint: '与 Agent 协作，查看任务进度与结果', icon: MessageSquare, keepAlive: false, render: () => null },
  { id: 'create', label: '创作空间', hint: '生成工具、应用、技能与工作流，管理扩展版本', icon: Sparkles, keepAlive: true, render: () => <CreationCenter /> },
  {
    id: 'tasks', label: '任务', hint: '运行中线程监控、定时自动唤醒 (Cron) 与后台任务队列', icon: CheckSquare, keepAlive: false,
    render: () => <TasksPanel />,
  },
  {
    id: 'extensions', label: '扩展', hint: '内置应用、能力、Skills、渠道', icon: Boxes, keepAlive: false,
    render: () => <ExtensionsPanel />,
  },
]

export const VIEW_BY_ID = new Map(VIEWS.map(v => [v.id, v]))
export const DEFAULT_VIEW_ID = 'chat'

export const WORKBENCH_VIEWS: ViewDef[] = [
  {id:'code',label:'代码',hint:'文件、编辑器、终端与变更审查',icon:Code2,keepAlive:true,
    render:ctx=><CodePanel sessionId={ctx.sessionId} onSendToAgent={sendToAgent}/>},
  {
    id: 'memory', label: '记忆', hint: '语义长效记忆与知识沉淀', icon: Brain, keepAlive: false,
    render: ctx => <MemoryPanel key={ctx.sessionId} onSendToAgent={sendToAgent} sessionId={ctx.sessionId} />,
  },
  {
    id: 'workflow', label: '工作流', hint: 'DAG 流程编排与自动化', icon: Workflow, keepAlive: true,
    render: () => <WorkflowPanel standalone={false} />,
  },
  {id:'apps',label:'应用',hint:'生成应用的交互预览',icon:AppWindow,keepAlive:true,
    render:()=>stageState.activeAppName ? <PreviewPanel key={stageState.activeAppName} appName={stageState.activeAppName}/> :
      <div class="workbench-empty"><AppWindow size={28}/><strong>尚未选择应用</strong><p>在创作空间生成或打开应用后，在这里查看预览。</p></div>},
]
export const WORKBENCH_BY_ID = new Map(WORKBENCH_VIEWS.map(v=>[v.id,v]))
