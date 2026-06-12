import { defineComponent, type PropType } from 'vue'
import type { WorkflowVariable } from '../../workflow/variableScope'

export default defineComponent({
  name: 'VariableRefField',
  props: {
    value: { type: String, default: '' },
    variables: { type: Array as PropType<WorkflowVariable[]>, default: () => [] },
    placeholder: { type: String, default: '' },
    invalid: { type: Boolean, default: false },
  },
  emits: ['update'],
  setup(props, { emit }) {
    const inputStyle = (invalid: boolean) => ({
      width: '100%',
      padding: '8px 10px',
      border: `1px solid ${invalid ? 'var(--error)' : 'var(--border)'}`,
      borderRadius: 'var(--radius-md)',
      fontSize: '12px',
      background: 'var(--input-bg)',
      color: 'var(--text)',
      outline: 'none',
      fontFamily: 'var(--font-mono)',
    })

    return () => (
      <div class="wf-ref-field">
        <div class="wf-ref-input-row">
          <input
            type="text"
            value={props.value}
            placeholder={props.placeholder || '{{last}}'}
            onInput={(e: Event) => emit('update', (e.target as HTMLInputElement).value)}
            style={inputStyle(props.invalid)}
          />
        </div>
        {props.variables.length > 0 && (
          <div class="wf-ref-chips">
            {props.variables.slice(0, 12).map(v => (
              <button
                key={v.key}
                type="button"
                class="wf-ref-chip"
                title={v.group}
                onClick={() => emit('update', v.key)}
              >{v.label}</button>
            ))}
          </div>
        )}
      </div>
    )
  },
})
