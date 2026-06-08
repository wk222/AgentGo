import { defineComponent, ref, watch, type PropType } from 'vue'
import type { WorkflowVariable } from '../../workflow/variableScope'
import {
  defaultBranchTable,
  parseBranchTable,
  stringifyBranchTable,
  type BranchRow,
} from '../../workflow/branchTable'
import { compileCondition, parseCondition } from '../../workflow/conditionModel'
import ConditionBuilder from './ConditionBuilder'

export default defineComponent({
  name: 'BranchTableEditor',
  props: {
    value: { type: String, default: '' },
    variables: { type: Array as PropType<WorkflowVariable[]>, default: () => [] },
    nodeId: { type: String, default: '' },
  },
  emits: ['update'],
  setup(props, { emit }) {
    const rows = ref<BranchRow[]>(parseBranchTable(props.value))

    watch(() => props.value, v => {
      rows.value = parseBranchTable(v)
    })

    const emitRows = () => {
      emit('update', stringifyBranchTable(rows.value))
    }

    const patchRow = (index: number, partial: Partial<BranchRow>) => {
      rows.value = rows.value.map((r, i) => (i === index ? { ...r, ...partial } : r))
      emitRows()
    }

    const patchCondition = (index: number, when: string) => {
      patchRow(index, { when, condition: parseCondition(when) })
    }

    const addRow = () => {
      const id = `branch_${Date.now()}`
      rows.value = [
        ...rows.value,
        {
          id,
          label: `分支 ${rows.value.length + 1}`,
          when: 'contains:',
          condition: { left: '{{last}}', op: 'contains', right: '' },
        },
      ]
      emitRows()
    }

    const removeRow = (index: number) => {
      if (rows.value.length <= 1) return
      rows.value = rows.value.filter((_, i) => i !== index)
      emitRows()
    }

    const inputStyle = {
      width: '100%',
      padding: '6px 8px',
      border: '1px solid var(--border)',
      borderRadius: 'var(--radius-md)',
      fontSize: '12px',
      background: 'var(--input-bg)',
    }

    return () => (
      <div class="wf-branch-table">
        <div class="wf-branch-table-head">
          <span>IF 分支表（与连线双写）</span>
          <button type="button" class="wf-when-preset-btn" onClick={addRow}>+ 分支</button>
        </div>
        {rows.value.map((row, i) => (
          <div key={row.id} class="wf-branch-row">
            <div class="wf-branch-row-top">
              <input
                value={row.label}
                placeholder="分支名"
                style={{ ...inputStyle, flex: 1 }}
                onInput={(e: Event) => patchRow(i, { label: (e.target as HTMLInputElement).value })}
              />
              <span class="wf-branch-handle-id">{row.id}</span>
              {rows.value.length > 1 && (
                <button type="button" class="wf-branch-del" onClick={() => removeRow(i)}>×</button>
              )}
            </div>
            <ConditionBuilder
              when={compileCondition(row.condition)}
              variables={props.variables}
              onUpdate={(w: string) => patchCondition(i, w)}
            />
            {row.target ? (
              <p class="wf-field-hint">目标节点 · {row.target}</p>
            ) : (
              <p class="wf-field-hint">从句柄 <b>{row.id}</b> 拖线以绑定目标</p>
            )}
            {row.isDefault ? <span class="wf-branch-default-tag">默认</span> : null}
          </div>
        ))}
      </div>
    )
  },
})
