import { defineComponent, ref, watch, type PropType } from 'vue'
import {
  CONDITION_OPS,
  compileCondition,
  parseCondition,
  type ConditionExpr,
} from '../../workflow/conditionModel'
import type { WorkflowVariable } from '../../workflow/variableScope'
import VariableRefField from './VariableRefField'

export default defineComponent({
  name: 'ConditionBuilder',
  props: {
    when: { type: String, default: '' },
    variables: { type: Array as PropType<WorkflowVariable[]>, default: () => [] },
  },
  emits: ['update'],
  setup(props, { emit }) {
    const expr = ref<ConditionExpr>(parseCondition(props.when))

    watch(() => props.when, w => {
      expr.value = parseCondition(w)
    })

    const patch = (partial: Partial<ConditionExpr>) => {
      expr.value = { ...expr.value, ...partial }
      emit('update', compileCondition(expr.value))
    }

    const opMeta = () => CONDITION_OPS.find(o => o.value === expr.value.op)

    const selectStyle = {
      width: '100%',
      padding: '8px 10px',
      border: '1px solid var(--border)',
      borderRadius: 'var(--radius-md)',
      fontSize: '12px',
      background: 'var(--input-bg)',
      color: 'var(--text)',
    }

    return () => (
      <div class="wf-condition-builder">
        <label class="wf-field">
          <span>左值（变量/文本）</span>
          <VariableRefField
            value={expr.value.left}
            variables={props.variables}
            placeholder="{{last}}"
            onUpdate={(v: string) => patch({ left: v })}
          />
        </label>
        <label class="wf-field">
          <span>运算符</span>
          <select
            value={expr.value.op}
            onChange={(e: Event) => patch({ op: (e.target as HTMLSelectElement).value as ConditionExpr['op'] })}
            style={selectStyle}
          >
            {CONDITION_OPS.map(o => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
          </select>
        </label>
        {opMeta()?.needsRight && (
          <label class="wf-field">
            <span>右值</span>
            <VariableRefField
              value={expr.value.right}
              variables={props.variables}
              placeholder="ok"
              onUpdate={(v: string) => patch({ right: v })}
            />
          </label>
        )}
        <p class="wf-field-hint">
          生成 when：<code class="wf-inline-code">{compileCondition(expr.value)}</code>
        </p>
      </div>
    )
  },
})
