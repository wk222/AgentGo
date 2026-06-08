import { defineComponent, shallowRef, markRaw, type PropType } from 'vue'
import {
  VueFlow,
  applyNodeChanges,
  applyEdgeChanges,
  type Node,
  type Edge,
  type NodeChange,
  type EdgeChange,
  type Connection,
} from '@vue-flow/core'
import { Background } from '@vue-flow/background'
import { Controls } from '@vue-flow/controls'
import WorkflowFlowNode from './WorkflowFlowNode'
import WorkflowNodePalette from './WorkflowNodePalette'
import FormMetaPanel from './workflow-form/FormMetaPanel'
import {
  flowgramToVueFlow,
  vueFlowToFlowgram,
  createFlowgramNode,
  flowgramNodeToVue,
  WF_NODE_TYPE,
  type WfNodeData,
  type WfEdgeData,
  type FlowgramDocument,
} from '../workflow/flowgramAdapter'

const nodeTypes = markRaw({ [WF_NODE_TYPE]: WorkflowFlowNode })

export default defineComponent({
  name: 'WorkflowSubCanvasModal',
  props: {
    title: { type: String, default: '子画布' },
    document: { type: Object as PropType<FlowgramDocument>, required: true },
  },
  emits: ['save', 'close'],
  setup(props, { emit }) {
    const g = flowgramToVueFlow(props.document)
    const nodes = shallowRef<Node<WfNodeData>[]>(g.nodes)
    const edges = shallowRef<Edge<WfEdgeData>[]>(g.edges)
    const selectedNodeId = shallowRef<string | null>(null)

    const selectedNode = () => nodes.value.find(n => n.id === selectedNodeId.value) || null

    const onNodesChange = (changes: NodeChange[]) => {
      nodes.value = applyNodeChanges(changes, nodes.value as any) as Node<WfNodeData>[]
    }
    const onEdgesChange = (changes: EdgeChange[]) => {
      edges.value = applyEdgeChanges(changes, edges.value as any) as Edge<WfEdgeData>[]
    }
    const onConnect = (conn: Connection) => {
      if (!conn.source || !conn.target || conn.source === conn.target) return
      edges.value = [
        ...edges.value,
        {
          id: `sub-e-${Date.now()}`,
          source: conn.source,
          target: conn.target,
          type: 'smoothstep',
          animated: true,
          style: { stroke: 'var(--accent)', strokeWidth: 2 },
        },
      ]
    }

    const addNode = (type: string) => {
      const fn = createFlowgramNode(type, { x: 80 + nodes.value.length * 40, y: 60 + nodes.value.length * 20 })
      nodes.value = [...nodes.value, flowgramNodeToVue(fn)]
      selectedNodeId.value = fn.id
    }

    const save = () => {
      emit('save', JSON.stringify(vueFlowToFlowgram(nodes.value, edges.value)))
    }

    return () => (
      <div class="wf-subcanvas-overlay" onClick={() => emit('close')}>
        <div class="wf-subcanvas-dialog" onClick={(e: Event) => e.stopPropagation()}>
          <div class="wf-subcanvas-head">
            <h3>{props.title}</h3>
            <div class="wf-subcanvas-actions">
              <button type="button" class="wf-secondary-btn" onClick={() => emit('close')}>取消</button>
              <button type="button" class="wf-secondary-btn wf-primary-btn" onClick={save}>保存子画布</button>
            </div>
          </div>
          <div class="wf-subcanvas-body">
            <WorkflowNodePalette onAdd={addNode} />
            <div class="wf-canvas-wrap">
              <VueFlow
                nodes={nodes.value}
                edges={edges.value}
                nodeTypes={nodeTypes}
                onNodesChange={onNodesChange}
                onEdgesChange={onEdgesChange}
                onConnect={onConnect}
                onNodeClick={({ node }: { node: Node }) => { selectedNodeId.value = node.id }}
                onPaneClick={() => { selectedNodeId.value = null }}
                fitViewOnInit
                nodesConnectable
                class="wf-vue-flow"
              >
                <Background gap={16} size={1} color="var(--border-subtle)" />
                <Controls showInteractive={false} />
              </VueFlow>
            </div>
            <FormMetaPanel
              node={selectedNode()}
              nodes={nodes.value}
              edges={edges.value}
              onUpdate={() => {}}
              onDelete={() => {
                const id = selectedNodeId.value
                if (!id) return
                nodes.value = nodes.value.filter(n => n.id !== id)
                edges.value = edges.value.filter(e => e.source !== id && e.target !== id)
                selectedNodeId.value = null
              }}
            />
          </div>
        </div>
      </div>
    )
  },
})
