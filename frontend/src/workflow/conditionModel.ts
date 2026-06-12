/** Coze-style structured condition → executor when string */

export type ConditionOp =
  | 'contains'
  | 'equals'
  | 'not_equals'
  | 'not_empty'
  | 'empty'
  | 'starts_with'
  | 'ends_with'

export interface ConditionExpr {
  left: string
  op: ConditionOp
  right: string
}

export const CONDITION_OPS: { value: ConditionOp; label: string; needsRight: boolean }[] = [
  { value: 'contains', label: '包含', needsRight: true },
  { value: 'equals', label: '等于', needsRight: true },
  { value: 'not_equals', label: '不等于', needsRight: true },
  { value: 'starts_with', label: '开头是', needsRight: true },
  { value: 'ends_with', label: '结尾是', needsRight: true },
  { value: 'not_empty', label: '非空', needsRight: false },
  { value: 'empty', label: '为空', needsRight: false },
]

export function defaultCondition(): ConditionExpr {
  return { left: '{{last}}', op: 'contains', right: 'ok' }
}

export function compileCondition(c: ConditionExpr): string {
  const left = (c.left || '{{last}}').trim()
  const right = (c.right || '').trim()
  switch (c.op) {
    case 'contains':
      return `contains:${right}`
    case 'equals':
      return `equals:${right}`
    case 'not_equals':
      return `not_equals:${right}`
    case 'starts_with':
      return `starts_with:${right}`
    case 'ends_with':
      return `ends_with:${right}`
    case 'not_empty':
      return 'true'
    case 'empty':
      return 'false'
    default:
      return right ? `contains:${right}` : 'true'
  }
}

/** Best-effort parse legacy when strings into builder state */
export function parseCondition(when: string): ConditionExpr {
  const w = (when || '').trim()
  if (!w) return defaultCondition()
  const lower = w.toLowerCase()
  if (lower === 'true' || lower === 'yes' || lower === '1') {
    return { left: '{{last}}', op: 'not_empty', right: '' }
  }
  if (lower === 'false' || lower === 'no' || lower === '0') {
    return { left: '{{last}}', op: 'empty', right: '' }
  }
  const prefixes: [ConditionOp, string][] = [
    ['contains', 'contains:'],
    ['equals', 'equals:'],
    ['not_equals', 'not_equals:'],
    ['starts_with', 'starts_with:'],
    ['ends_with', 'ends_with:'],
  ]
  for (const [op, pre] of prefixes) {
    if (lower.startsWith(pre)) {
      return { left: '{{last}}', op, right: w.slice(pre.length) }
    }
  }
  return { left: '{{last}}', op: 'contains', right: w }
}
