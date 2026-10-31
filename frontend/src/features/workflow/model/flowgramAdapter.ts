import { Position, type Edge, type Node } from '@vue-flow/core'
import { catalogEntry, normalizeCatalogType } from './nodeCatalog'
import { whenFromSourceHandle } from './branchUtils'
import { emptySubCanvas, stringifySubCanvas } from './subCanvas'
import { defaultBranchTable, stringifyBranchTable } from './branchTable'

export const WF_NODE_TYPE = 'wf'

export interface FlowgramNode {
  id: string
  type: string
  title?: string
  x?: number
  y?: number
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
  runStatus?: 'idle' | 'running' | 'success' | 'error'
}

export interface WfEdgeData {
  when: string
  /** Structured condition (Coze-style); when is compiled cache for executor */
  condition?: import('./conditionModel').ConditionExpr
}

const EDGE_LABEL_STYLE = { fill: 'var(--text-strong)', fontSize: 11, fontWeight: 600 }
const EDGE_LABEL_BG = { fill: 'var(--surface)', fillOpacity: 0.94 }

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
    labelBgBorderRadius: 6,
    data: { when },
    style: { stroke: '#4e40e5', strokeWidth: 2 },
  }
}

/** Default Coze-style canvas for new workflows */
export function emptyFlowgramDoc(): FlowgramDocument {
  return {
    nodes: [
      { id: 'start', type: 'Start', meta: { position: { x: 80, y: 160 } }, data: { description: '接收用户输入参数' } },
      { id: 'llm1', type: 'LLM', meta: { position: { x: 430, y: 140 } }, data: { prompt: '{{input}}' } },
      { id: 'end', type: 'End', meta: { position: { x: 790, y: 160 } }, data: { output_var: '{{last}}' } },
    ],
    edges: [
      { sourceNodeID: 'start', targetNodeID: 'llm1' },
      { sourceNodeID: 'llm1', targetNodeID: 'end' },
    ],
  }
}

/**
 * Coze Studio style topological DAG auto-layout (Left-to-Right layered rank layout).
 * Guarantees Start is on the left, End is on the right, and no two nodes stack on (0,0).
 */
export function autoLayoutVueNodes(
  nodes: Node<WfNodeData>[],
  edges: Edge<WfEdgeData>[],
): Node<WfNodeData>[] {
  if (!nodes.length) return nodes

  const rank = new Map<string, number>()
  const inDegree = new Map<string, number>()
  const adj = new Map<string, string[]>()

  for (const n of nodes) {
    inDegree.set(n.id, 0)
    adj.set(n.id, [])
    const ft = (n.data as WfNodeData)?.flowType
    if (ft === 'Start') rank.set(n.id, 0)
  }

  for (const e of edges) {
    if (!adj.has(e.source) || !inDegree.has(e.target)) continue
    adj.get(e.source)!.push(e.target)
    inDegree.set(e.target, (inDegree.get(e.target) || 0) + 1)
  }

  const queue: string[] = []
  for (const n of nodes) {
    if ((inDegree.get(n.id) || 0) === 0) {
      queue.push(n.id)
      if (!rank.has(n.id)) {
        const ft = (n.data as WfNodeData)?.flowType
        rank.set(n.id, ft === 'Start' ? 0 : 1)
      }
    }
  }

  const visited = new Set<string>()
  while (queue.length > 0) {
    const u = queue.shift()!
    visited.add(u)
    const r = rank.get(u) ?? 0
    for (const v of adj.get(u) || []) {
      const nextRank = Math.max(rank.get(v) ?? 0, r + 1)
      rank.set(v, nextRank)
      inDegree.set(v, (inDegree.get(v) || 1) - 1)
      if ((inDegree.get(v) || 0) <= 0 && !visited.has(v)) {
        queue.push(v)
      }
    }
  }

  let maxRank = 0
  for (const r of rank.values()) {
    if (r > maxRank) maxRank = r
  }

  // Assign unvisited or End nodes sensibly
  let orphanIdx = 1
  for (const n of nodes) {
    const ft = (n.data as WfNodeData)?.flowType
    if (!rank.has(n.id)) {
      if (ft === 'End') {
        rank.set(n.id, Math.max(maxRank + 1, 2))
      } else {
        rank.set(n.id, orphanIdx++)
      }
    } else if (ft === 'End' && nodes.length > 1) {
      rank.set(n.id, Math.max(rank.get(n.id)!, maxRank))
    }
  }

  // Group by rank column
  const columns = new Map<number, Node<WfNodeData>[]>()
  for (const n of nodes) {
    const c = rank.get(n.id) ?? 0
    if (!columns.has(c)) columns.set(c, [])
    columns.get(c)!.push(n)
  }

  const sortedCols = Array.from(columns.keys()).sort((a, b) => a - b)
  const COL_X_GAP = 350
  const ROW_Y_GAP = 185
  const START_X = 80
  const CENTER_Y = 180

  const posMap = new Map<string, { x: number; y: number }>()
  sortedCols.forEach((colKey, colIndex) => {
    const colNodes = columns.get(colKey)!
    const totalHeight = (colNodes.length - 1) * ROW_Y_GAP
    const startY = Math.max(60, CENTER_Y - totalHeight / 2)
    colNodes.forEach((n, rowIndex) => {
      posMap.set(n.id, {
        x: START_X + colIndex * COL_X_GAP,
        y: Math.round(startY + rowIndex * ROW_Y_GAP),
      })
    })
  })

  return nodes.map(n => {
    const p = posMap.get(n.id)
    return p ? { ...n, position: p } : n
  })
}

export function flowgramToVueFlow(doc: FlowgramDocument): { nodes: Node<WfNodeData>[]; edges: Edge<WfEdgeData>[] } {
  const rawNodes = doc.nodes || []
  const rawEdges = doc.edges || []

  // Auto-heal legacy broken 2-node (start+end, 0 edges at 0,0) workflows into a complete Coze 3-node flow
  if (
    rawNodes.length === 2 &&
    rawEdges.length === 0 &&
    rawNodes.some(n => normalizeCatalogType(n.type) === 'Start') &&
    rawNodes.some(n => normalizeCatalogType(n.type) === 'End')
  ) {
    return flowgramToVueFlow(emptyFlowgramDoc())
  }

  let nodes: Node<WfNodeData>[] = rawNodes.map(n => {
    const type = normalizeCatalogType(n.type || 'LLM')
    const entry = catalogEntry(type)
    const fields: Record<string, string> = {}
    if (n.data) {
      for (const [k, v] of Object.entries(n.data)) {
        if (v != null) fields[k] = String(v)
      }
    }
    const rawX = n.meta?.position?.x ?? (typeof n.x === 'number' ? n.x : 0)
    const rawY = n.meta?.position?.y ?? (typeof n.y === 'number' ? n.y : 0)
    const customLabel =
      (typeof n.title === 'string' && n.title.trim()) ||
      (typeof fields.title === 'string' && fields.title.trim()) ||
      entry.label

    return {
      id: n.id,
      type: WF_NODE_TYPE,
      position: { x: rawX, y: rawY },
      data: { flowType: type, label: customLabel, fields },
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
    }
  })

  const nodesById = new Map(nodes.map(n => [n.id, n.data as WfNodeData]))
  const edges: Edge<WfEdgeData>[] = (doc.edges || []).map((e, i) => buildVueEdge(e, i, nodesById))

  // Detect overlapping or tight legacy nodes (Coze rich cards are 268px wide)
  if (nodes.length >= 2) {
    let hasCollision = false
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const dx = Math.abs(nodes[i].position.x - nodes[j].position.x)
        const dy = Math.abs(nodes[i].position.y - nodes[j].position.y)
        if (dx < 290 && dy < 110) {
          hasCollision = true
          break
        }
      }
      if (hasCollision) break
    }
    if (hasCollision) {
      nodes = autoLayoutVueNodes(nodes, edges)
    }
  }

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
        type: normalizeCatalogType(d?.flowType || 'LLM'),
        meta: { position: { x: Math.round(n.position.x), y: Math.round(n.position.y) } },
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
  const normType = normalizeCatalogType(type)
  const entry = catalogEntry(normType)
  const id = nextNodeId(normType.toLowerCase().replace(/[^a-z]/g, '') + '_')
  const data: Record<string, string> = { ...(entry.defaults || {}) }
  if ((normType === 'Loop' || normType === 'Batch' || normType === 'ForEach') && !data.sub_canvas) {
    data.sub_canvas = stringifySubCanvas(emptySubCanvas())
  }
  if ((normType === 'IF' || normType === 'Branch') && !data.branch_table) {
    data.branch_table = stringifyBranchTable(defaultBranchTable())
  }
  return {
    id,
    type: normType,
    meta: { position },
    data: Object.keys(data).length ? data : {},
  }
}

export function flowgramNodeToVue(n: FlowgramNode): Node<WfNodeData> {
  return flowgramToVueFlow({ nodes: [n], edges: [] }).nodes[0]
}
