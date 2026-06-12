import { Position, type Edge, type Node } from '@vue-flow/core'
import { catalogEntry } from './nodeCatalog'
import { whenFromSourceHandle } from './branchUtils'
import { emptySubCanvas, stringifySubCanvas } from './subCanvas'
import { defaultBranchTable, stringifyBranchTable } from './branchTable'

export const WF_NODE_TYPE = 'wf'

export interface FlowgramNode {
  id: string
  type: string
  meta: { position: { x: number; y: number } }
  data?: Record<string, unknown>
}

export interface FlowgramEdge {
  sourceNodeID: string
  targetNodeID: string
  sourcePortID?: string
  when?: string
  label?: string
}

export interface FlowgramDocument {
  nodes: FlowgramNode[]
  edges: FlowgramEdge[]
}

export interface WfNodeData {
  flowType: string
  label: string
  fields: Record<string, string>
}

export interface WfEdgeData {
  when: string
  /** Structured condition (Coze-style); when is compiled cache for executor */
  condition?: import('./conditionModel').ConditionExpr
}

const EDGE_LABEL_STYLE = { fill: 'var(--text-strong)', fontSize: 11, fontWeight: 600 }
const EDGE_LABEL_BG = { fill: 'var(--surface)', fillOpacity: 0.92 }

let nodeSeq = 0

export function nextNodeId(prefix: string) {
  nodeSeq += 1
  return `${prefix}${nodeSeq}`
}

function edgeWhen(e: FlowgramEdge): string {
  return String(e.when || e.label || e.sourcePortID || '').trim()
}

export function buildVueEdge(e: FlowgramEdge, index: number, nodesById: Map<string, WfNodeData>): Edge<WfEdgeData> {
  let when = edgeWhen(e)
  const src = nodesById.get(e.sourceNodeID)
  if (!when && e.sourcePortID && src) {
    when = whenFromSourceHandle(src.flowType, e.sourcePortID)
  }
  return {
    id: `e-${e.sourceNodeID}-${e.targetNodeID}-${index}`,
    source: e.sourceNodeID,
    target: e.targetNodeID,
    sourceHandle: e.sourcePortID || undefined,
    targetHandle: 'in',
    type: 'smoothstep',
    animated: true,
    label: when || undefined,
    labelStyle: EDGE_LABEL_STYLE,
    labelBgStyle: EDGE_LABEL_BG,
    labelBgPadding: [4, 8] as [number, number],
    labelBgBorderRadius: 4,
    data: { when },
    style: { stroke: 'var(--accent)', strokeWidth: 2 },
  }
}

/** Default canvas for new workflows */
export function emptyFlowgramDoc(): FlowgramDocument {
  return {
    nodes: [
      { id: 'start', type: 'Start', meta: { position: { x: 80, y: 120 } }, data: {} },
      { id: 'llm1', type: 'LLM', meta: { position: { x: 320, y: 120 } }, data: { prompt: '{{input}}' } },
      { id: 'end', type: 'End', meta: { position: { x: 560, y: 120 } }, data: {} },
    ],
    edges: [
      { sourceNodeID: 'start', targetNodeID: 'llm1' },
      { sourceNodeID: 'llm1', targetNodeID: 'end' },
    ],
  }
}

export function flowgramToVueFlow(doc: FlowgramDocument): { nodes: Node<WfNodeData>[]; edges: Edge<WfEdgeData>[] } {
  const nodes: Node<WfNodeData>[] = (doc.nodes || []).map(n => {
    const type = n.type || 'LLM'
    const entry = catalogEntry(type)
    const fields: Record<string, string> = {}
    if (n.data) {
      for (const [k, v] of Object.entries(n.data)) {
        if (v != null) fields[k] = String(v)
      }
    }
    return {
      id: n.id,
      type: WF_NODE_TYPE,
      position: { x: n.meta?.position?.x ?? 0, y: n.meta?.position?.y ?? 0 },
      data: { flowType: type, label: entry.label, fields },
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
    }
  })
  const nodesById = new Map(nodes.map(n => [n.id, n.data as WfNodeData]))
  const edges: Edge<WfEdgeData>[] = (doc.edges || []).map((e, i) => buildVueEdge(e, i, nodesById))
  return { nodes, edges }
}

export function vueFlowToFlowgram(nodes: Node<WfNodeData>[], edges: Edge<WfEdgeData>[]): FlowgramDocument {
  return {
    nodes: nodes.map(n => {
      const d = n.data as WfNodeData
      const data: Record<string, unknown> = {}
      if (d?.fields) {
        for (const [k, v] of Object.entries(d.fields)) {
          if (v !== '') data[k] = v
        }
      }
      return {
        id: n.id,
        type: d?.flowType || 'LLM',
        meta: { position: { x: n.position.x, y: n.position.y } },
        data: Object.keys(data).length ? data : undefined,
      }
    }),
    edges: edges.map(e => {
      const d = e.data as WfEdgeData | undefined
      const when = String(d?.when || e.label || '').trim()
      const fe: FlowgramEdge = {
        sourceNodeID: e.source,
        targetNodeID: e.target,
      }
      if (e.sourceHandle) fe.sourcePortID = e.sourceHandle
      if (when) {
        fe.when = when
        fe.label = when
      }
      return fe
    }),
  }
}

export function createFlowgramNode(type: string, position: { x: number; y: number }): FlowgramNode {
  const entry = catalogEntry(type)
  const id = nextNodeId(type.toLowerCase().replace(/[^a-z]/g, '') + '_')
  const data: Record<string, string> = { ...(entry.defaults || {}) }
  if ((type === 'Loop' || type === 'Batch' || type === 'ForEach') && !data.sub_canvas) {
    data.sub_canvas = stringifySubCanvas(emptySubCanvas())
  }
  if ((type === 'IF' || type === 'Branch') && !data.branch_table) {
    data.branch_table = stringifyBranchTable(defaultBranchTable())
  }
  return {
    id,
    type,
    meta: { position },
    data: Object.keys(data).length ? data : {},
  }
}

export function flowgramNodeToVue(n: FlowgramNode): Node<WfNodeData> {
  return flowgramToVueFlow({ nodes: [n], edges: [] }).nodes[0]
}
