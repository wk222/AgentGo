import { defineComponent, computed, ref, watch, type PropType } from 'vue'
import type { Edge, Node } from '@vue-flow/core'
import type { WfNodeData } from '../../workflow/flowgramAdapter'
import { formMetaForType, validateNodeForm, type FormFieldMeta } from '../../workflow/formMeta'
import { collectVariablesForNode } from '../../workflow/variableScope'
import { parseSubCanvas } from '../../workflow/subCanvas'
import VariableRefField from './VariableRefField'
import BranchTableEditor from './BranchTableEditor'
import WorkflowSubCanvasModal from '../WorkflowSubCanvasModal'

export default defineComponent({
  name: 'FormMetaPanel',
  props: {
    node: { type: Object as PropType<Node<WfNodeData> | null>, default: null },
    nodes: { type: Array as PropType<Node<WfNodeData>[]>, default: () => [] },
    edges: { type: Array as PropType<Edge[]>, default: () => [] },
  },
  emits: ['update', 'delete', 'sync-edges', 'syncEdges'],
  setup(props, { emit }) {
    const showSubCanvas = ref(false)
    const errors = ref<Record<string, string>>({})

    const flowType = computed(() => (props.node?.data as WfNodeData)?.flowType || '')
    const meta = computed(() => formMetaForType(flowType.value))
    const variables = computed(() => {
      if (!props.node) return []
      return collectVariablesForNode(props.node.id, props.nodes, props.edges)
    })

    const revalidate = () => {
      if (!props.node?.data) {
        errors.value = {}
        return
      }
      const d = props.node.data as WfNodeData
      errors.value = validateNodeForm(flowType.value, d.fields || {})
    }

    watch(() => props.node?.id, revalidate, { immediate: true })

    const setField = (key: string, value: string) => {
      const n = props.node
      if (!n?.data) return
      const d = n.data as WfNodeData
      if (!d.fields) d.fields = {}
      d.fields[key] = value
      revalidate()
      emit('update')
    }

    const getField = (key: string) => {
      const d = props.node?.data as WfNodeData | undefined
      return d?.fields?.[key] ?? ''
    }

    const canDelete = computed(() => flowType.value !== 'Start')
    const hasSubCanvas = computed(() =>
      flowType.value === 'Loop' || flowType.value === 'Batch' || flowType.value === 'ForEach',
    )

    const inputStyle = (key: string) => ({
      width: '100%',
      padding: '8px 10px',
      border: `1px solid ${errors.value[key] ? 'var(--error)' : 'var(--border)'}`,
      borderRadius: 'var(--radius-md)',
      fontSize: '12px',
      background: 'var(--input-bg)',
      color: 'var(--text)',
      outline: 'none',
      fontFamily: 'inherit',
    })

    const emitSyncEdges = () => {
      emit('sync-edges')
      emit('syncEdges')
    }

    const renderField = (f: FormFieldMeta) => {
      const val = getField(f.key)
      const err = errors.value[f.key]
      if (f.type === 'sub_canvas') {
        const doc = parseSubCanvas(val)
        return (
          <div class="wf-subcanvas-trigger">
            <button type="button" class="wf-secondary-btn" onClick={() => { showSubCanvas.value = true }}>
              编辑子画布（{doc.nodes.length} 节点）
            </button>
            {showSubCanvas.value && (
              <WorkflowSubCanvasModal
                title={
                  flowType.value === 'Batch' ? '批处理体'
                    : flowType.value === 'ForEach' ? 'ForEach 迭代体'
                      : '循环体'
                }
                document={doc}
                onSave={(json: string) => {
                  setField(f.key, json)
                  showSubCanvas.value = false
                }}
                onClose={() => { showSubCanvas.value = false }}
              />
            )}
          </div>
        )
      }
      if (f.type === 'branch_table') {
        return (
          <BranchTableEditor
            value={val}
            nodeId={props.node?.id || ''}
            variables={variables.value}
            onUpdate={(v: string) => {
              setField(f.key, v)
              emitSyncEdges()
            }}
          />
        )
      }
      if (f.type === 'ref') {
        return (
          <VariableRefField
            value={val}
            variables={variables.value}
            placeholder={f.placeholder}
            invalid={!!err}
            onUpdate={(v: string) => setField(f.key, v)}
          />
        )
      }
      if (f.type === 'select') {
        return (
          <select
            value={val}
            onChange={(e: Event) => setField(f.key, (e.target as HTMLSelectElement).value)}
            style={inputStyle(f.key)}
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
            style={inputStyle(f.key)}
          />
        )
      }
      if (f.type === 'textarea' || f.type === 'code' || f.type === 'json') {
        const mono = f.type === 'code' || f.type === 'json'
        return (
          <textarea
            rows={f.type === 'code' || f.type === 'json' ? 6 : 3}
            value={val}
            placeholder={f.placeholder}
            onInput={(e: Event) => setField(f.key, (e.target as HTMLTextAreaElement).value)}
            style={{
              ...inputStyle(f.key),
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
          style={inputStyle(f.key)}
        />
      )
    }

    return () => (
      <aside class="wf-node-props">
        {!props.node ? (
          <div class="wf-node-props-empty-panel">
            <div class="wf-node-props-empty-title">节点配置</div>
            <p>点击画布节点编辑 form-meta 参数</p>
            <p class="wf-node-props-tip">支持变量引用选择器 · 分支/连线条件构建器 · 循环子画布</p>
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
            {hasSubCanvas.value && (
              <p class="wf-node-props-badge">含子画布容器</p>
            )}
            {meta.value.fields.length === 0 ? (
              <p class="wf-node-props-empty">此节点无额外参数</p>
            ) : (
              meta.value.fields.map(f => (
                <label key={f.key} class="wf-field">
                  <span>
                    {f.label}
                    {f.required ? <span class="wf-required">*</span> : null}
                  </span>
                  {renderField(f)}
                  {errors.value[f.key] ? (
                    <span class="wf-field-error">{errors.value[f.key]}</span>
                  ) : f.hint ? (
                    <span class="wf-field-hint">{f.hint}</span>
                  ) : null}
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
