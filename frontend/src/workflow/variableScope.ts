import type { Edge, Node } from '@vue-flow/core'
import type { WfNodeData } from './flowgramAdapter'

export interface WorkflowVariable {
  key: string
  label: string
  group: string
  sourceNodeId?: string
}

const GLOBAL_VARS: WorkflowVariable[] = [
  { key: '{{input}}', label: '工作流输入', group: '全局' },
  { key: '{{last}}', label: '上一步输出', group: '全局' },
]

/** Collect variables available to a node (Coze variableService subset). */
export function collectVariablesForNode(
  nodeId: string,
  nodes: Node<WfNodeData>[],
  edges: Edge[],
): WorkflowVariable[] {
  const upstream = upstreamNodeIds(nodeId, edges)
  const vars: WorkflowVariable[] = [...GLOBAL_VARS]
  const seen = new Set(vars.map(v => v.key))

  for (const id of upstream) {
    const n = nodes.find(x => x.id === id)
    if (!n?.data) continue
    const d = n.data as WfNodeData
    const title = d.label || id
    const outs = outputsForNodeType(d)
    for (const o of outs) {
      const key = o.key.replace('{{node}}', `{{${id}}}`)
      if (seen.has(key)) continue
      seen.add(key)
      vars.push({
        key,
        label: `${title} · ${o.label}`,
        group: title,
        sourceNodeId: id,
      })
    }
    const name = d.fields?.name?.trim()
    if (name && (d.flowType === 'Variable' || d.flowType === 'SetVariable')) {
      const vk = `{{var.${name}}}`
      if (!seen.has(vk)) {
        seen.add(vk)
        vars.push({ key: vk, label: `变量 ${name}`, group: '变量', sourceNodeId: id })
      }
    }
  }
  return vars
}

function upstreamNodeIds(nodeId: string, edges: Edge[]): string[] {
  const rev = new Map<string, string[]>()
  for (const e of edges) {
    const list = rev.get(e.target) || []
    list.push(e.source)
    rev.set(e.target, list)
  }
  const out: string[] = []
  const q = [...(rev.get(nodeId) || [])]
  const seen = new Set<string>()
  while (q.length) {
    const id = q.shift()!
    if (seen.has(id)) continue
    seen.add(id)
    out.push(id)
    for (const p of rev.get(id) || []) q.push(p)
  }
  return out
}

function outputsForNodeType(d: WfNodeData): { key: string; label: string }[] {
  switch (d.flowType) {
    case 'Start':
      return [{ key: '{{input}}', label: '输入' }]
    case 'LLM':
    case 'Agent':
    case 'Tool':
    case 'Code':
    case 'HTTP':
    case 'Bash':
      return [{ key: '{{node.output}}', label: '输出' }]
    case 'Variable':
      return d.fields?.name
        ? [{ key: `{{var.${d.fields.name}}}`, label: d.fields.name }]
        : []
    case 'JSON':
      return [{ key: '{{node.output}}', label: '解析结果' }]
    default:
      return [{ key: '{{node.output}}', label: '输出' }]
  }
}
