import type { Connection } from '@vue-flow/core'
import { isBranchFlowType } from './branchUtils'

export type PortKind = 'control_in' | 'control_out' | 'branch_out' | 'loop_body_in' | 'loop_body_out'

export function portKind(handleId: string | null | undefined, isSource: boolean, flowType: string): PortKind {
  const h = (handleId || '').trim()
  if (flowType === 'Loop' || flowType === 'Batch') {
    if (isSource && h === 'loop_body') return 'loop_body_out'
    if (!isSource && h === 'loop_body_in') return 'loop_body_in'
  }
  if (isSource) {
    if (isBranchFlowType(flowType) && h && h !== 'out') return 'branch_out'
    return 'control_out'
  }
  return 'control_in'
}

export interface ConnectCheck {
  ok: boolean
  reason?: string
}

/** Coze-like port compatibility (control flow only in v1). */
export function validateConnection(
  conn: Connection,
  sourceFlowType: string,
  targetFlowType: string,
): ConnectCheck {
  if (!conn.source || !conn.target) return { ok: false, reason: '无效连接' }
  if (conn.source === conn.target) return { ok: false, reason: '不能连接自身' }
  if (targetFlowType === 'Start') return { ok: false, reason: '开始节点不能作为目标' }
  if (sourceFlowType === 'End') return { ok: false, reason: '结束节点不能作为源' }

  const src = portKind(conn.sourceHandle, true, sourceFlowType)
  const tgt = portKind(conn.targetHandle, false, targetFlowType)

  if (src === 'loop_body_out') {
    if (tgt !== 'loop_body_in' && tgt !== 'control_in') {
      return { ok: false, reason: '循环体出口只能连循环体内部或主流程' }
    }
  }
  if (src === 'branch_out' || src === 'control_out') {
    if (tgt === 'loop_body_in') {
      return { ok: false, reason: '主流程不能直接连入循环体入口' }
    }
  }
  return { ok: true }
}
