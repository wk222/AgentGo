/** Coze-style form-meta: declarative fields + validation per node type. */

export type FormFieldType =
  | 'text'
  | 'textarea'
  | 'number'
  | 'select'
  | 'json'
  | 'code'
  | 'ref'
  | 'sub_canvas'
  | 'branch_table'

export interface FormFieldMeta {
  key: string
  label: string
  type: FormFieldType
  required?: boolean
  min?: number
  max?: number
  pattern?: string
  patternMessage?: string
  options?: { value: string; label: string }[]
  placeholder?: string
  hint?: string
}

export interface NodeFormMeta {
  fields: FormFieldMeta[]
}

export interface ValidateContext {
  flowType: string
  allFields: Record<string, string>
}

export function validateField(meta: FormFieldMeta, value: string, _ctx: ValidateContext): string | undefined {
  const v = value ?? ''
  if (meta.required && !v.trim()) return `${meta.label} 为必填项`
  if (!v.trim()) return undefined
  if (meta.type === 'number') {
    const n = Number(v)
    if (Number.isNaN(n)) return `${meta.label} 必须是数字`
    if (meta.min != null && n < meta.min) return `${meta.label} 不能小于 ${meta.min}`
    if (meta.max != null && n > meta.max) return `${meta.label} 不能大于 ${meta.max}`
  }
  if (meta.pattern) {
    try {
      if (!new RegExp(meta.pattern).test(v)) {
        return meta.patternMessage || `${meta.label} 格式不正确`
      }
    } catch { /* ignore bad pattern */ }
  }
  if (meta.type === 'json') {
    try {
      JSON.parse(v)
    } catch {
      return `${meta.label} 必须是合法 JSON`
    }
  }
  return undefined
}

export function validateNodeForm(flowType: string, fields: Record<string, string>): Record<string, string> {
  const meta = NODE_FORM_META[flowType]
  if (!meta) return {}
  const ctx: ValidateContext = { flowType, allFields: fields }
  const errors: Record<string, string> = {}
  for (const f of meta.fields) {
    const err = validateField(f, fields[f.key] ?? '', ctx)
    if (err) errors[f.key] = err
  }
  return errors
}

const ref = (key: string, label: string, opts?: Partial<FormFieldMeta>): FormFieldMeta => ({
  key,
  label,
  type: 'ref',
  placeholder: '{{last}}',
  ...opts,
})

export const NODE_FORM_META: Record<string, NodeFormMeta> = {
  Start: {
    fields: [
      { key: 'description', label: '描述', type: 'text', hint: '工作流入口说明' },
    ],
  },
  End: {
    fields: [
      { key: 'description', label: '描述', type: 'text' },
      ref('output_var', '输出变量', { hint: '留空则返回最后一步输出' }),
    ],
  },
  LLM: {
    fields: [
      ref('prompt', 'Prompt', { required: true, hint: '支持变量引用' }),
      ref('systemPrompt', 'System Prompt', { type: 'ref' }),
    ],
  },
  Tool: {
    fields: [
      { key: 'tool_name', label: '工具名', type: 'text', required: true },
      { key: 'args', label: '参数 JSON', type: 'json', placeholder: '{}' },
    ],
  },
  Agent: {
    fields: [
      { key: 'agent_name', label: 'Agent', type: 'text' },
      ref('prompt', '任务', { required: true }),
    ],
  },
  Code: {
    fields: [
      ref('code', '模板', { type: 'ref', required: true }),
    ],
  },
  HTTP: {
    fields: [
      {
        key: 'method',
        label: 'Method',
        type: 'select',
        options: ['GET', 'POST', 'PUT', 'DELETE', 'PATCH'].map(v => ({ value: v, label: v })),
      },
      { key: 'url', label: 'URL', type: 'ref', required: true, pattern: '^https?://', patternMessage: 'URL 需以 http:// 或 https:// 开头' },
      ref('body', 'Body'),
    ],
  },
  IF: {
    fields: [
      { key: 'branch_table', label: '分支条件表', type: 'branch_table', required: true },
      ref('expression', '兜底表达式', { hint: '无分支命中时使用（可选）' }),
    ],
  },
  Branch: {
    fields: [
      { key: 'branch_table', label: '分支条件表', type: 'branch_table', required: true },
      ref('expression', '兜底表达式', { hint: '与 IF 相同（兼容 Branch）' }),
    ],
  },
  Parallel: {
    fields: [
      { key: 'max_workers', label: '最大并发', type: 'number', min: 1, max: 16, placeholder: '4' },
    ],
  },
  Merge: {
    fields: [
      {
        key: 'merge_strategy',
        label: '汇合策略',
        type: 'select',
        required: true,
        options: [
          { value: 'json_array', label: 'JSON 数组' },
          { value: 'concat', label: '文本拼接' },
          { value: 'first', label: '取第一个' },
          { value: 'last', label: '取最后一个' },
        ],
      },
      {
        key: 'wait_for_all',
        label: '等待全部分支',
        type: 'select',
        options: [
          { value: 'true', label: '是（Parallel 默认）' },
          { value: 'false', label: '否' },
        ],
      },
    ],
  },
  ForEach: {
    fields: [
      { key: 'collection_var', label: '集合变量名', type: 'text', required: true, placeholder: 'items' },
      { key: 'item_var', label: '项变量名', type: 'text', required: true, placeholder: 'item' },
      { key: 'max_iterations', label: '最大轮次', type: 'number', min: 1, max: 500, placeholder: '100' },
      { key: 'sub_canvas', label: '迭代体', type: 'sub_canvas', required: true },
    ],
  },
  Loop: {
    fields: [
      ref('collection', '循环数组', { required: true, hint: 'JSON 数组或变量，如 {{last}}' }),
      { key: 'item_var', label: '项变量名', type: 'text', required: true, placeholder: 'item' },
      { key: 'max_iterations', label: '最大轮次', type: 'number', min: 1, max: 500, placeholder: '100' },
      { key: 'sub_canvas', label: '循环体', type: 'sub_canvas', required: true, hint: '点击编辑子画布' },
    ],
  },
  Batch: {
    fields: [
      ref('collection', '批处理数组', { required: true }),
      { key: 'batch_size', label: '批大小', type: 'number', required: true, min: 1, max: 50, placeholder: '5' },
      { key: 'item_var', label: '项变量名', type: 'text', required: true, placeholder: 'batch' },
      { key: 'sub_canvas', label: '批处理体', type: 'sub_canvas', required: true },
    ],
  },
  Delay: {
    fields: [{ key: 'seconds', label: '延迟（秒）', type: 'number', required: true, min: 0, max: 3600 }],
  },
  Variable: {
    fields: [
      { key: 'name', label: '变量名', type: 'text', required: true, pattern: '^[a-zA-Z_][a-zA-Z0-9_]*$', patternMessage: '变量名需为合法标识符' },
      ref('value', '值', { required: true }),
    ],
  },
  JSON: {
    fields: [{ key: 'field', label: '字段路径', type: 'text', placeholder: 'result' }],
  },
  Subworkflow: {
    fields: [{ key: 'workflow_id', label: '子工作流 ID', type: 'text', required: true }],
  },
  AskUser: {
    fields: [ref('prompt', '提问', { required: true })],
  },
  Bash: {
    fields: [{ key: 'script', label: '命令', type: 'code', required: true }],
  },
  Notify: {
    fields: [
      { key: 'channel', label: '渠道', type: 'text' },
      ref('message', '消息', { required: true }),
    ],
  },
  Monitor: {
    fields: [
      { key: 'metric', label: '指标', type: 'text' },
      { key: 'threshold', label: '阈值', type: 'text' },
    ],
  },
  DataSource: {
    fields: [
      { key: 'path', label: '文件路径', type: 'text' },
      { key: 'data', label: '内联数据', type: 'textarea' },
    ],
  },
  Debate: {
    fields: [
      ref('topic', '议题', { required: true }),
      { key: 'rounds', label: '轮数', type: 'number', min: 1, max: 10 },
    ],
  },
}

export function formMetaForType(flowType: string): NodeFormMeta {
  return NODE_FORM_META[flowType] || { fields: [] }
}

/** @deprecated use formMetaForType */
export function fieldsForType(flowType: string) {
  return formMetaForType(flowType).fields
}
