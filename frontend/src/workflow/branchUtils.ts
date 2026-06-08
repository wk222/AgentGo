import { catalogEntry } from './nodeCatalog'
import { branchHandleIds, parseBranchTable } from './branchTable'

const DEFAULT_BRANCHES = ['if_true', 'if_else']

/** Parse branch outlet handle ids (Coze IF branch table first). */
export function parseBranchHandles(fields?: Record<string, string>): string[] {
  if (fields?.branch_table?.trim()) {
    return branchHandleIds(parseBranchTable(fields.branch_table))
  }
  const raw = fields?.branches?.trim()
  if (raw) {
    try {
      const arr = JSON.parse(raw)
      if (Array.isArray(arr) && arr.length > 0) {
        return arr.map(v => String(v).trim()).filter(Boolean)
      }
    } catch { /* ignore */ }
  }
  return [...DEFAULT_BRANCHES]
}

export function isBranchFlowType(flowType: string): boolean {
  return catalogEntry(flowType).isBranch === true ||
    flowType === 'Branch' || flowType === 'IF'
}

/** Suggest when condition from branch handle id when user connects. */
export function whenFromSourceHandle(flowType: string, sourceHandle?: string | null): string {
  if (!sourceHandle || !isBranchFlowType(flowType)) return ''
  const h = sourceHandle.trim()
  if (h === 'true') return 'true'
  if (h === 'false') return 'false'
  if (h === 'yes') return 'yes'
  if (h === 'no') return 'no'
  return h
}

export const WHEN_PRESETS = [
  { value: 'true', label: 'true（有输出）' },
  { value: 'false', label: 'false（空输出）' },
  { value: 'contains:ok', label: 'contains:ok' },
  { value: 'equals:done', label: 'equals:done' },
  { value: 'yes', label: 'yes' },
  { value: 'no', label: 'no' },
]
