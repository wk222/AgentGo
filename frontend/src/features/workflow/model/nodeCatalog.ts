/** Flowgram / Coze Studio canvas node types — aligned with internal/workflow/executor.go */

export type NodeCategory = 'control' | 'action' | 'data' | 'flow'

export interface NodeCatalogEntry {
  type: string
  label: string
  description: string
  color: string
  category: NodeCategory
  icon?: string
  defaults?: Record<string, string>
  outputVar?: string
  outputType?: string
  addable?: boolean
  /** Branch nodes may have multiple outgoing edges */
  isBranch?: boolean
}

export const NODE_CATALOG: NodeCatalogEntry[] = [
  {
    type: 'Start',
    label: '开始',
    description: '工作流起始节点，定义初始输入参数',
    color: '#10b981',
    category: 'control',
    icon: '▶',
    outputVar: 'input',
    outputType: 'String',
    addable: false,
  },
  {
    type: 'End',
    label: '结束',
    description: '工作流终点，汇总并返回最终输出变量',
    color: '#ef4444',
    category: 'control',
    icon: '■',
    outputVar: 'result',
    outputType: 'String',
  },
  {
    type: 'LLM',
    label: '大模型 LLM',
    description: '调用大语言模型，根据提示词与上下文生成回答',
    color: '#4e40e5',
    category: 'action',
    icon: '✦',
    outputVar: 'output',
    outputType: 'String',
    defaults: { prompt: '{{input}}' },
  },
  {
    type: 'Tool',
    label: '工具调用',
    description: '执行内置工具或 MCP 扩展能力',
    color: '#f59e0b',
    category: 'action',
    icon: '⚙',
    outputVar: 'tool_output',
    outputType: 'JSON',
    defaults: { tool_name: 'get_current_time', args: '{}' },
  },
  {
    type: 'Agent',
    label: '子智能体',
    description: '委派独立 Agent 执行多步推理与工具协作',
    color: '#7c3aed',
    category: 'action',
    icon: '◎',
    outputVar: 'agent_reply',
    outputType: 'String',
    defaults: { agent_name: 'assistant', prompt: '{{input}}' },
  },
  {
    type: 'Code',
    label: '代码 / 模板',
    description: '通过模板或脚本转换、拼接上下文数据',
    color: '#2563eb',
    category: 'action',
    icon: '{}',
    outputVar: 'code_out',
    outputType: 'String',
    defaults: { code: '{{last}}' },
  },
  {
    type: 'HTTP',
    label: 'HTTP 请求',
    description: '发送 REST API 请求获取或推送外部数据',
    color: '#0ea5e9',
    category: 'action',
    icon: '⇄',
    outputVar: 'response',
    outputType: 'Object',
    defaults: { method: 'GET', url: 'https://', body: '' },
  },
  {
    type: 'Bash',
    label: 'Shell 命令',
    description: '在受控工作区终端执行本地脚本或命令',
    color: '#475569',
    category: 'action',
    icon: '$_',
    outputVar: 'stdout',
    outputType: 'String',
    defaults: { script: '' },
  },
  {
    type: 'Debate',
    label: '多 Agent 辩论',
    description: '多个智能体多轮交叉辩论并收敛最优结论',
    color: '#9333ea',
    category: 'action',
    icon: '⚖',
    outputVar: 'consensus',
    outputType: 'String',
    defaults: { topic: '{{input}}', rounds: '2' },
  },
  {
    type: 'IF',
    label: '选择器 (IF)',
    description: '按条件表达式将流程路由到不同分支',
    color: '#f97316',
    category: 'control',
    icon: '⑂',
    isBranch: true,
    outputVar: 'branch',
    outputType: 'Branch',
    defaults: { expression: '', branch_table: '' },
  },
  {
    type: 'Branch',
    label: '多路分支',
    description: '多条件规则匹配并路由至对应下游节点',
    color: '#fb923c',
    category: 'control',
    icon: '⑃',
    isBranch: true,
    outputVar: 'matched',
    outputType: 'Branch',
    defaults: { expression: '', branch_table: '' },
  },
  {
    type: 'Parallel',
    label: '并行执行',
    description: '同时并发启动多个下游分支任务',
    color: '#3b82f6',
    category: 'control',
    icon: '∥',
    outputVar: 'branches',
    outputType: 'Array',
    defaults: { max_workers: '4' },
  },
  {
    type: 'Merge',
    label: '聚合汇合',
    description: '等待多个并行分支完成并聚合输出',
    color: '#6366f1',
    category: 'control',
    icon: '∩',
    outputVar: 'merged',
    outputType: 'Array',
    defaults: { merge_strategy: 'json_array', wait_for_all: 'true' },
  },
  {
    type: 'Loop',
    label: '循环 (Loop)',
    description: '在嵌套子画布中循环处理集合元素',
    color: '#16a34a',
    category: 'control',
    icon: '↻',
    outputVar: 'results',
    outputType: 'Array',
    defaults: { collection: '{{last}}', item_var: 'item', max_iterations: '100', sub_canvas: '' },
  },
  {
    type: 'ForEach',
    label: '遍历 (ForEach)',
    description: '对数组每一项执行子画布逻辑',
    color: '#059669',
    category: 'control',
    icon: '∀',
    outputVar: 'items_out',
    outputType: 'Array',
    defaults: { collection_var: 'items', item_var: 'item', max_iterations: '100', sub_canvas: '' },
  },
  {
    type: 'Batch',
    label: '批处理 (Batch)',
    description: '按指定批次大小分块并行执行子画布',
    color: '#15803d',
    category: 'control',
    icon: '⊞',
    outputVar: 'batches',
    outputType: 'Array',
    defaults: { collection: '{{last}}', batch_size: '5', item_var: 'batch', sub_canvas: '' },
  },
  {
    type: 'Delay',
    label: '延时等待',
    description: '暂停等待指定秒数后继续执行下游',
    color: '#a855f7',
    category: 'control',
    icon: '⏱',
    outputVar: 'after_delay',
    outputType: 'Signal',
    defaults: { seconds: '1' },
  },
  {
    type: 'Variable',
    label: '变量赋值',
    description: '设置或更新工作流上下文变量',
    color: '#06b6d4',
    category: 'data',
    icon: '{x}',
    outputVar: 'var1',
    outputType: 'Any',
    defaults: { name: 'var1', value: '{{last}}' },
  },
  {
    type: 'JSON',
    label: 'JSON 提取',
    description: '解析 JSON 字符串并提取指定路径字段',
    color: '#14b8a6',
    category: 'data',
    icon: '{;}',
    outputVar: 'parsed',
    outputType: 'Any',
    defaults: { field: '' },
  },
  {
    type: 'Monitor',
    label: '指标监控',
    description: '检测关键数值指标与阈值告警条件',
    color: '#64748b',
    category: 'data',
    icon: '📊',
    outputVar: 'metric_ok',
    outputType: 'Boolean',
    defaults: { metric: '', threshold: '' },
  },
  {
    type: 'DataSource',
    label: '数据源读取',
    description: '加载本地文件或数据源内容到工作流',
    color: '#0891b2',
    category: 'data',
    icon: '📁',
    outputVar: 'dataset',
    outputType: 'String',
    defaults: { path: '', data: '' },
  },
  {
    type: 'Subworkflow',
    label: '子工作流',
    description: '嵌套调用另一个已发布的工作流',
    color: '#8b5cf6',
    category: 'flow',
    icon: '↪',
    outputVar: 'sub_output',
    outputType: 'String',
    defaults: { workflow_id: '' },
  },
  {
    type: 'AskUser',
    label: '人工确认 (HITL)',
    description: '挂起工作流并向用户发起交互确认或提问',
    color: '#ec4899',
    category: 'flow',
    icon: '👋',
    outputVar: 'user_reply',
    outputType: 'String',
    defaults: { prompt: '请确认是否继续' },
  },
  {
    type: 'Notify',
    label: '消息通知',
    description: '向桌面或外部渠道发送运行结果通知',
    color: '#6366f1',
    category: 'flow',
    icon: '🔔',
    outputVar: 'notified',
    outputType: 'Boolean',
    defaults: { channel: 'desktop', message: '{{last}}' },
  },
  {
    type: 'Custom',
    label: '自定义节点',
    description: '运行智能体发布的可复用节点（有明确的输入/输出契约，背后可以是工具、整套工作流或 Python）',
    color: '#0d9488',
    category: 'flow',
    icon: '⬡',
    outputVar: 'node_output',
    outputType: 'JSON',
    defaults: { node_id: '', inputs: '{}' },
  },
]

export const CATEGORY_LABELS: Record<NodeCategory, string> = {
  action: 'AI 与核心组件',
  control: '业务逻辑与控制',
  data: '数据与变量处理',
  flow: '子流程与人工协同',
}

export const ADDABLE_NODES = NODE_CATALOG.filter(n => n.addable !== false && n.type !== 'Start')

export const PALETTE_GROUPS = (['action', 'control', 'data', 'flow'] as NodeCategory[]).map(cat => ({
  category: cat,
  label: CATEGORY_LABELS[cat],
  nodes: ADDABLE_NODES.filter(n => n.category === cat),
}))

const TYPE_ALIAS_MAP: Record<string, string> = {
  start: 'Start',
  end: 'End',
  llm: 'LLM',
  model: 'LLM',
  tool: 'Tool',
  plugin: 'Tool',
  agent: 'Agent',
  adk_agent: 'Agent',
  coding_agent: 'Agent',
  codingagent: 'Agent',
  code: 'Code',
  http: 'HTTP',
  bash: 'Bash',
  shell: 'Bash',
  debate: 'Debate',
  multi_agent: 'Debate',
  if: 'IF',
  condition: 'IF',
  branch: 'Branch',
  switch: 'Branch',
  parallel: 'Parallel',
  merge: 'Merge',
  loop: 'Loop',
  foreach: 'ForEach',
  batch: 'Batch',
  delay: 'Delay',
  variable: 'Variable',
  set_var: 'Variable',
  json: 'JSON',
  monitor: 'Monitor',
  data_source: 'DataSource',
  datasource: 'DataSource',
  subworkflow: 'Subworkflow',
  workflow: 'Subworkflow',
  ask_user: 'AskUser',
  askuser: 'AskUser',
  human: 'AskUser',
  notify: 'Notify',
  notification: 'Notify',
  custom: 'Custom',
  custom_node: 'Custom',
}

export function normalizeCatalogType(rawType?: string): string {
  const trimmed = String(rawType || '').trim()
  if (!trimmed) return 'LLM'
  const exact = NODE_CATALOG.find(n => n.type === trimmed)
  if (exact) return exact.type
  // 'custom:<id>' is how a definition names a custom node; the canvas type is 'Custom'
  if (trimmed.toLowerCase().startsWith('custom:')) return 'Custom'
  const mapped = TYPE_ALIAS_MAP[trimmed.toLowerCase()]
  if (mapped) return mapped
  const ci = NODE_CATALOG.find(n => n.type.toLowerCase() === trimmed.toLowerCase())
  return ci ? ci.type : 'LLM'
}

export function catalogEntry(type: string): NodeCatalogEntry {
  const norm = normalizeCatalogType(type)
  return NODE_CATALOG.find(n => n.type === norm) || NODE_CATALOG.find(n => n.type === 'LLM')!
}

export function isTerminalType(type: string) {
  const norm = normalizeCatalogType(type)
  return norm === 'Start' || norm === 'End'
}
