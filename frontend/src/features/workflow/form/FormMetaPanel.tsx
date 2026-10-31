import { defineComponent, computed, ref, watch, type PropType } from 'vue'
import type { Edge, Node } from '@vue-flow/core'
import { Trash2, X, Copy, Check } from 'lucide-vue-next'
import type { WfNodeData } from '../model/flowgramAdapter'
import { catalogEntry, normalizeCatalogType } from '../model/nodeCatalog'
import { formMetaForType, validateNodeForm, type FormFieldMeta } from '../model/formMeta'
import { collectVariablesForNode } from '../model/variableScope'
import { parseSubCanvas } from '../model/subCanvas'
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
  emits: ['update', 'delete', 'close', 'sync-edges', 'syncEdges'],
  setup(props, { emit }) {
    const showSubCanvas = ref(false)
    const errors = ref<Record<string, string>>({})
    const copiedVar = ref('')

    const flowType = computed(() => normalizeCatalogType((props.node?.data as WfNodeData)?.flowType || ''))
    const entry = computed(() => catalogEntry(flowType.value))
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

    const setLabel = (nextLabel: string) => {
      const n = props.node
      if (!n?.data) return
      const d = n.data as WfNodeData
      d.label = nextLabel
      if (!d.fields) d.fields = {}
      d.fields.title = nextLabel
      emit('update')
    }

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

    const copyVarToken = async (token: string) => {
      try {
        await navigator.clipboard.writeText(token)
        copiedVar.value = token
        setTimeout(() => {
          if (copiedVar.value === token) copiedVar.value = ''
        }, 1500)
      } catch {}
    }

    const canDelete = computed(() => flowType.value !== 'Start')

    const inputStyle = (key: string) => ({
      width: '100%',
      padding: '8px 10px',
      border: `1px solid ${errors.value[key] ? 'var(--error)' : 'var(--border)'}`,
      borderRadius: '8px',
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
            <button type="button" class="coze-subcanvas-open-btn" onClick={() => { showSubCanvas.value = true }}>
              <span>◫ 打开嵌套子画布编辑器</span>
              <span class="coze-subcanvas-count">{doc.nodes.length} 节点</span>
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
            rows={f.type === 'code' || f.type === 'json' ? 6 : 4}
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

    return () => {
      if (!props.node) return null
      const d = props.node.data as WfNodeData
      const cat = entry.value

      return (
        <aside class="wf-node-props coze-inspector-drawer" style={{ '--node-accent': cat.color } as any}>
          <div class="coze-inspector-header">
            <div class="coze-inspector-head-top">
              <span class="coze-inspector-icon" style={{ background: cat.color }}>
                {cat.icon || '✦'}
              </span>
              <input
                type="text"
                class="coze-inspector-title-input"
                value={d.label || cat.label}
                title="点击修改节点名称"
                onInput={(e: Event) => setLabel((e.target as HTMLInputElement).value)}
              />
              <span class="coze-inspector-id-pill">#{props.node.id}</span>
              {canDelete.value && (
                <button
                  type="button"
                  class="coze-inspector-icon-btn danger"
                  title="删除此节点 (Delete)"
                  onClick={() => emit('delete')}
                >
                  <Trash2 size={14} />
                </button>
              )}
              <button
                type="button"
                class="coze-inspector-icon-btn"
                title="收起配置抽屉"
                onClick={() => emit('close')}
              >
                <X size={15} />
              </button>
            </div>
            <p class="coze-inspector-desc">{cat.description}</p>
          </div>

          <div class="coze-inspector-scroll">
            {/* Section 1: Upstream Input Variables */}
            {variables.value.length > 0 && (
              <div class="coze-inspector-section">
                <div class="coze-inspector-sec-head">
                  <span class="coze-inspector-sec-title">输入 · 上游可用变量</span>
                  <span class="coze-inspector-sec-sub">点击复制引用</span>
                </div>
                <div class="coze-inspector-var-list">
                  {variables.value.map(v => (
                    <button
                      key={v.key}
                      type="button"
                      class="coze-inspector-var-chip"
                      title={`${v.label} → ${v.key}`}
                      onClick={() => copyVarToken(v.key)}
                    >
                      <span class="coze-var-brace">{'{x}'}</span>
                      <span class="coze-var-chip-token">{v.key}</span>
                      {copiedVar.value === v.key ? <Check size={11} /> : <Copy size={10} />}
                    </button>
                  ))}
                </div>
              </div>
            )}

            {/* Section 2: Node Form Meta Parameters */}
            <div class="coze-inspector-section">
              <div class="coze-inspector-sec-head">
                <span class="coze-inspector-sec-title">参数配置 ({flowType.value})</span>
              </div>
              {meta.value.fields.length === 0 ? (
                <p class="wf-node-props-empty">此节点无需额外配置参数</p>
              ) : (
                <div class="coze-inspector-fields">
                  {meta.value.fields.map(f => (
                    <label key={f.key} class="wf-field coze-field-card">
                      <span class="coze-field-label">
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
                  ))}
                </div>
              )}
            </div>

            {/* Section 3: Output Variables */}
            <div class="coze-inspector-section">
              <div class="coze-inspector-sec-head">
                <span class="coze-inspector-sec-title">输出变量</span>
                <span class="coze-inspector-sec-sub">供下游节点引用</span>
              </div>
              <div class="coze-inspector-output-card">
                <div class="coze-output-row">
                  <span class="coze-var-pill">
                    <span class="coze-var-brace">{'{x}'}</span>
                    <span class="coze-var-name">{cat.outputVar || 'output'}</span>
                    <em class="coze-var-type">{cat.outputType || 'String'}</em>
                  </span>
                  <code class="coze-output-ref">{`{{${props.node.id}.output}}`}</code>
                </div>
                <div class="coze-output-hint">
                  紧邻的下游节点也可直接使用 <code>{'{{last}}'}</code> 引用本节点输出。
                </div>
              </div>
            </div>
          </div>
        </aside>
      )
    }
  },
})
