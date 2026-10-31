import { defineComponent, computed, type PropType } from 'vue'
import type { Edge, Node } from '@vue-flow/core'
import { Trash2, X } from 'lucide-vue-next'
import type { WfEdgeData, WfNodeData } from './model/flowgramAdapter'
import { collectVariablesForNode } from './model/variableScope'
import ConditionBuilder from './form/ConditionBuilder'

export default defineComponent({
  name: 'WorkflowEdgeProps',
  props: {
    edge: { type: Object as PropType<Edge<WfEdgeData> | null>, default: null },
    nodes: { type: Array as PropType<Node<WfNodeData>[]>, default: () => [] },
    edges: { type: Array as PropType<Edge[]>, default: () => [] },
    sourceLabel: { type: String, default: '' },
    targetLabel: { type: String, default: '' },
  },
  emits: ['update', 'delete', 'close'],
  setup(props, { emit }) {
    const when = computed(() => (props.edge?.data as WfEdgeData)?.when ?? '')

    const variables = computed(() => {
      if (!props.edge?.source) return []
      return collectVariablesForNode(props.edge.source, props.nodes, props.edges)
    })

    const setWhen = (value: string) => {
      emit('update', value)
    }

    return () => {
      if (!props.edge) return null
      return (
        <aside class="wf-node-props wf-edge-props coze-inspector-drawer">
          <div class="coze-inspector-header">
            <div class="coze-inspector-head-top">
              <span class="coze-inspector-icon" style={{ background: '#4e40e5' }}>⇄</span>
              <span class="coze-inspector-edge-title">连线与分支条件</span>
              <button
                type="button"
                class="coze-inspector-icon-btn danger"
                title="删除此连线"
                onClick={() => emit('delete')}
              >
                <Trash2 size={14} />
              </button>
              <button
                type="button"
                class="coze-inspector-icon-btn"
                title="收起面板"
                onClick={() => emit('close')}
              >
                <X size={15} />
              </button>
            </div>
            <p class="coze-inspector-desc">
              {props.sourceLabel || props.edge.source}
              <span class="wf-edge-arrow"> → </span>
              {props.targetLabel || props.edge.target}
              {props.edge.sourceHandle ? ` (端口: ${props.edge.sourceHandle})` : ''}
            </p>
          </div>
          <div class="coze-inspector-scroll">
            <div class="coze-inspector-section">
              <ConditionBuilder
                when={when.value}
                variables={variables.value}
                onUpdate={setWhen}
              />
            </div>
          </div>
        </aside>
      )
    }
  },
})
