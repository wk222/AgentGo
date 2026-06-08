import type { Edge } from '@vue-flow/core'
import {
  compileCondition,
  defaultCondition,
  parseCondition,
  type ConditionExpr,
} from './conditionModel'
import type { WfEdgeData } from './flowgramAdapter'

export interface BranchRow {
  id: string
  label: string
  when: string
  condition: ConditionExpr
  target?: string
  isDefault?: boolean
}

export function defaultBranchTable(): BranchRow[] {
  return [
    {
      id: 'if_true',
      label: '分支 1',
      when: 'contains:ok',
      condition: { left: '{{last}}', op: 'contains', right: 'ok' },
    },
    {
      id: 'if_else',
      label: '否则',
      when: 'true',
      condition: { left: '{{last}}', op: 'not_empty', right: '' },
      isDefault: true,
    },
  ]
}

export function parseBranchTable(raw?: string): BranchRow[] {
  if (!raw?.trim()) return defaultBranchTable()
  try {
    const arr = JSON.parse(raw) as BranchRow[]
    if (!Array.isArray(arr) || !arr.length) return defaultBranchTable()
    return arr.map((r, i) => ({
      id: r.id || `branch_${i}`,
      label: r.label || `分支 ${i + 1}`,
      when: r.when || compileCondition(r.condition || defaultCondition()),
      condition: r.condition || parseCondition(r.when),
      target: r.target,
      isDefault: !!r.isDefault,
    }))
  } catch {
    return defaultBranchTable()
  }
}

export function stringifyBranchTable(rows: BranchRow[]): string {
  return JSON.stringify(rows.map(r => ({
    ...r,
    when: compileCondition(r.condition),
  })))
}

export function branchHandleIds(rows: BranchRow[]): string[] {
  return rows.map(r => r.id)
}

/** Node branch_table → outgoing edges (Coze dual-write). */
export function syncEdgesFromBranchTable(
  nodeId: string,
  rows: BranchRow[],
  edges: Edge<WfEdgeData>[],
): Edge<WfEdgeData>[] {
  const out = edges.map(e => ({ ...e, data: e.data ? { ...e.data } : { when: '' } }))
  for (const row of rows) {
    const when = compileCondition(row.condition)
    const idx = out.findIndex(e =>
      e.source === nodeId &&
      ((row.target && e.target === row.target) || (e.sourceHandle === row.id)),
    )
    if (idx >= 0) {
      out[idx] = {
        ...out[idx],
        sourceHandle: row.id,
        label: row.label || when,
        data: { when, condition: row.condition },
      }
    }
  }
  return out
}

/** Edge update → branch_table row (dual-write). */
export function syncBranchTableFromEdge(
  nodeId: string,
  edge: Edge<WfEdgeData>,
  rawTable: string,
): string {
  const rows = parseBranchTable(rawTable)
  const d = edge.data as WfEdgeData | undefined
  const when = d?.when || String(edge.label || '')
  const condition = d?.condition || parseCondition(when)
  let hit = false
  const next = rows.map(r => {
    if (
      (edge.sourceHandle && r.id === edge.sourceHandle) ||
      (r.target && r.target === edge.target)
    ) {
      hit = true
      return {
        ...r,
        target: edge.target,
        when,
        condition,
      }
    }
    return r
  })
  if (!hit) {
    next.push({
      id: edge.sourceHandle || `branch_${next.length}`,
      label: when || `→ ${edge.target}`,
      when,
      condition,
      target: edge.target,
    })
  }
  return stringifyBranchTable(next)
}

/** New connection from IF → attach target to branch row. */
export function attachTargetToBranchRow(
  rawTable: string,
  sourceHandle: string | null | undefined,
  targetId: string,
  when: string,
): string {
  const rows = parseBranchTable(rawTable)
  const condition = parseCondition(when)
  const hid = sourceHandle || `branch_${rows.length}`
  let found = false
  const next = rows.map(r => {
    if (r.id === hid) {
      found = true
      return { ...r, target: targetId, when, condition }
    }
    return r
  })
  if (!found) {
    next.push({
      id: hid,
      label: `→ ${targetId}`,
      when,
      condition,
      target: targetId,
    })
  }
  return stringifyBranchTable(next)
}
