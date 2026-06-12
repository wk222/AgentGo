import { defineComponent, computed, type PropType } from 'vue'
import type { Edge, Node } from '@vue-flow/core'
import type { WfEdgeData, WfNodeData } from '../workflow/flowgramAdapter'
import { collectVariablesForNode } from '../workflow/variableScope'
import ConditionBuilder from './workflow-form/ConditionBuilder'

export default defineComponent({
  name: 'WorkflowEdgeProps',
  props: {
    edge: { type: Object as PropType<Edge<WfEdgeData> | null>, default: null },
    nodes: { type: Array as PropType<Node<WfNodeData>[]>, default: () => [] },
    edges: { type: Array as PropType<Edge[]>, default: () => [] },
    sourceLabel: { type: String, default: '' },
    targetLabel: { type: String, default: '' },
  },
  emits: ['update', 'delete'],
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
        <aside class="wf-node-props wf-edge-props">
          <div class="wf-node-props-head">
            <h4>分支条件</h4>
          </div>
          <p class="wf-node-props-type">
            {props.sourceLabel || props.edge.source}
            <span class="wf-edge-arrow"> → </span>
            {props.targetLabel || props.edge.target}
          </p>
          {props.edge.sourceHandle ? (
            <p class="wf-edge-port">出口 · {props.edge.sourceHandle}</p>
          ) : null}
          <ConditionBuilder
            when={when.value}
            variables={variables.value}
            onUpdate={setWhen}
          />
          <button type="button" class="wf-delete-btn" onClick={() => emit('delete')}>
            删除此连线
          </button>
        </aside>
      )
    }
  },
})
