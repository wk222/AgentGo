/** Flowgram canvas node types — aligned with internal/workflow/executor.go */

export type NodeCategory = 'control' | 'action' | 'data' | 'flow'

export interface NodeCatalogEntry {
  type: string
  label: string
  color: string
  category: NodeCategory
  icon?: string
  defaults?: Record<string, string>
  addable?: boolean
  /** Branch nodes may have multiple outgoing edges */
  isBranch?: boolean
}

export const NODE_CATALOG: NodeCatalogEntry[] = [
  { type: 'Start', label: '开始', color: '#10b981', category: 'control', icon: '▶', addable: false },
  { type: 'End', label: '结束', color: '#ef4444', category: 'control', icon: '■' },
  { type: 'LLM', label: 'LLM', color: '#6366f1', category: 'action', icon: '◇', defaults: { prompt: '{{input}}' } },
  { type: 'Tool', label: '工具', color: '#f59e0b', category: 'action', icon: '⚙', defaults: { tool_name: 'get_current_time', args: '{}' } },
  { type: 'Agent', label: '子 Agent', color: '#7c3aed', category: 'action', icon: '◎', defaults: { agent_name: '', prompt: '{{input}}' } },
  { type: 'Code', label: '代码/模板', color: '#3b82f6', category: 'action', icon: '{}', defaults: { code: '{{last}}' } },
  { type: 'HTTP', label: 'HTTP', color: '#0ea5e9', category: 'action', icon: '⇄', defaults: { method: 'GET', url: 'https://', body: '' } },
  { type: 'Bash', label: 'Bash', color: '#475569', category: 'action', icon: '$', defaults: { script: '' } },
  { type: 'IF', label: 'IF 条件', color: '#f97316', category: 'control', icon: 'IF', isBranch: true, defaults: { expression: '', branch_table: '' } },
  { type: 'Branch', label: '条件分支', color: '#fb923c', category: 'control', icon: '⑂', isBranch: true, defaults: { expression: '', branch_table: '' } },
  { type: 'Parallel', label: '并行', color: '#3b82f6', category: 'control', icon: '∥', defaults: { max_workers: '4' } },
  { type: 'Merge', label: '汇合', color: '#6366f1', category: 'control', icon: '∩', defaults: { merge_strategy: 'json_array', wait_for_all: 'true' } },
  { type: 'ForEach', label: 'ForEach', color: '#059669', category: 'control', icon: '∀', defaults: { collection_var: 'items', item_var: 'item', max_iterations: '100', sub_canvas: '' } },
  { type: 'Loop', label: '循环', color: '#22c55e', category: 'control', icon: '↻', defaults: { collection: '{{last}}', item_var: 'item', max_iterations: '100', sub_canvas: '' } },
  { type: 'Batch', label: '批处理', color: '#16a34a', category: 'control', icon: '⊞', defaults: { collection: '{{last}}', batch_size: '5', item_var: 'batch', sub_canvas: '' } },
  { type: 'Delay', label: '延迟', color: '#a855f7', category: 'control', icon: '⏱', defaults: { seconds: '1' } },
  { type: 'Variable', label: '设变量', color: '#06b6d4', category: 'data', icon: 'x=', defaults: { name: 'var1', value: '{{last}}' } },
  { type: 'JSON', label: 'JSON 解析', color: '#14b8a6', category: 'data', icon: '{ }', defaults: { field: '' } },
  { type: 'Subworkflow', label: '子工作流', color: '#8b5cf6', category: 'flow', icon: '↪', defaults: { workflow_id: '' } },
  { type: 'AskUser', label: '人工确认', color: '#ec4899', category: 'flow', icon: '?', defaults: { prompt: '请确认是否继续' } },
  { type: 'Notify', label: '通知', color: '#8b5cf6', category: 'flow', icon: '🔔', defaults: { channel: 'desktop', message: '{{last}}' } },
  { type: 'Monitor', label: '监控', color: '#64748b', category: 'data', icon: '📊', defaults: { metric: '', threshold: '' } },
  { type: 'DataSource', label: '数据源', color: '#0891b2', category: 'data', icon: '📁', defaults: { path: '', data: '' } },
  { type: 'Debate', label: '多 Agent 辩论', color: '#9333ea', category: 'action', icon: '⚖', defaults: { topic: '{{input}}', rounds: '2' } },
]

export const CATEGORY_LABELS: Record<NodeCategory, string> = {
  control: '控制',
  action: '动作',
  data: '数据',
  flow: '流程',
}

export const ADDABLE_NODES = NODE_CATALOG.filter(n => n.addable !== false && n.type !== 'Start')

export const PALETTE_GROUPS = (['control', 'action', 'data', 'flow'] as NodeCategory[]).map(cat => ({
  category: cat,
  label: CATEGORY_LABELS[cat],
  nodes: ADDABLE_NODES.filter(n => n.category === cat),
}))

export function catalogEntry(type: string): NodeCatalogEntry {
  return NODE_CATALOG.find(n => n.type === type) || NODE_CATALOG.find(n => n.type === 'LLM')!
}

export function isTerminalType(type: string) {
  return type === 'Start' || type === 'End'
}
