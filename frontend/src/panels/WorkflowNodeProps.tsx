import { defineComponent, computed, type PropType } from 'vue'
import type { Node } from '@vue-flow/core'
import type { WfNodeData } from '../workflow/flowgramAdapter'
import { fieldsForType, type FieldDef } from '../workflow/nodeSchemas'

export default defineComponent({
  name: 'WorkflowNodeProps',
  props: {
    node: { type: Object as PropType<Node<WfNodeData> | null>, default: null },
  },
  emits: ['update', 'delete'],
  setup(props, { emit }) {
    const flowType = computed(() => (props.node?.data as WfNodeData)?.flowType || '')
    const schema = computed(() => fieldsForType(flowType.value))

    const setField = (key: string, value: string) => {
      const n = props.node
      if (!n?.data) return
      const d = n.data as WfNodeData
      if (!d.fields) d.fields = {}
      d.fields[key] = value
      emit('update')
    }

    const getField = (key: string) => {
      const d = props.node?.data as WfNodeData | undefined
      return d?.fields?.[key] ?? ''
    }

    const canDelete = computed(() => flowType.value !== 'Start')

    const inputStyle = {
      width: '100%',
      padding: '8px 10px',
      border: '1px solid var(--border)',
      borderRadius: 'var(--radius-md)',
      fontSize: '12px',
      background: 'var(--input-bg)',
      color: 'var(--text)',
      outline: 'none',
      fontFamily: 'inherit',
    }

    const renderField = (f: FieldDef) => {
      const val = getField(f.key)
      if (f.type === 'select') {
        return (
          <select
            value={val}
            onChange={(e: Event) => setField(f.key, (e.target as HTMLSelectElement).value)}
            style={inputStyle}
          >
            {(f.options || []).map(opt => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
        )
      }
      if (f.type === 'number') {
        return (
          <input
            type="number"
            value={val}
            placeholder={f.placeholder}
            onInput={(e: Event) => setField(f.key, (e.target as HTMLInputElement).value)}
            style={inputStyle}
          />
        )
      }
      if (f.type === 'textarea' || f.type === 'code' || f.type === 'json') {
        const mono = f.type === 'code' || f.type === 'json'
        return (
          <textarea
            rows={f.type === 'code' || f.type === 'json' ? 8 : 4}
            value={val}
            placeholder={f.placeholder}
            onInput={(e: Event) => setField(f.key, (e.target as HTMLTextAreaElement).value)}
            style={{
              ...inputStyle,
              resize: 'vertical' as const,
              fontFamily: mono ? 'var(--font-mono)' : 'inherit',
            }}
          />
        )
      }
      return (
        <input
          type="text"
          value={val}
          placeholder={f.placeholder}
          onInput={(e: Event) => setField(f.key, (e.target as HTMLInputElement).value)}
          style={inputStyle}
        />
      )
    }

    return () => (
      <aside class="wf-node-props">
        {!props.node ? (
          <div class="wf-node-props-empty-panel">
            <div class="wf-node-props-empty-title">节点配置</div>
            <p>点击画布上的节点以编辑参数</p>
            <p class="wf-node-props-tip">从节点右侧圆点拖到另一节点左侧圆点可连线；点击连线可编辑 when 条件</p>
          </div>
        ) : (
          <>
            <div class="wf-node-props-head">
              <h4>
                {(props.node.data as WfNodeData).label}
                <span class="wf-node-props-id">{props.node.id}</span>
              </h4>
            </div>
            <p class="wf-node-props-type">类型 · {flowType.value}</p>
            {schema.value.length === 0 ? (
              <p class="wf-node-props-empty">此节点无额外参数</p>
            ) : (
              schema.value.map(f => (
                <label key={f.key} class="wf-field">
                  <span>{f.label}</span>
                  {renderField(f)}
                  {f.hint ? <span class="wf-field-hint">{f.hint}</span> : null}
                </label>
              ))
            )}
            {canDelete.value && (
              <button type="button" class="wf-delete-btn" onClick={() => emit('delete')}>
                删除节点
              </button>
            )}
          </>
        )}
      </aside>
    )
  },
})
