import { defineComponent, ref, shallowRef, onMounted, watch, nextTick, markRaw } from 'vue'
import {
  VueFlow,
  useVueFlow,
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
import '@vue-flow/core/dist/style.css'
import '@vue-flow/core/dist/theme-default.css'
import '@vue-flow/controls/dist/style.css'
import { wailsCall } from '../wails'
import FormMetaPanel from './workflow-form/FormMetaPanel'
import WorkflowEdgeProps from './WorkflowEdgeProps'
import WorkflowNodePalette from './WorkflowNodePalette'
import WorkflowFlowNode from './WorkflowFlowNode'
import { isBranchFlowType, whenFromSourceHandle } from '../workflow/branchUtils'
import {
  attachTargetToBranchRow,
  syncBranchTableFromEdge,
  syncEdgesFromBranchTable,
  parseBranchTable,
} from '../workflow/branchTable'
import { validateConnection } from '../workflow/portTypes'
import { parseCondition } from '../workflow/conditionModel'
import {
  flowgramToVueFlow,
  vueFlowToFlowgram,
  emptyFlowgramDoc,
  createFlowgramNode,
  flowgramNodeToVue,
  WF_NODE_TYPE,
  type WfNodeData,
  type WfEdgeData,
  type FlowgramDocument,
} from '../workflow/flowgramAdapter'

const nodeTypes = markRaw({ [WF_NODE_TYPE]: WorkflowFlowNode })

/** 须在 VueFlow 子树内调用 useVueFlow */
const FitViewHelper = defineComponent({
  name: 'FitViewHelper',
  props: {
    nodeCount: { type: Number, required: true },
  },
  setup(props, { expose }) {
    const { fitView } = useVueFlow()
    const doFit = () => {
      nextTick(() => {
        try {
          fitView({ padding: 0.2, duration: 200 })
        } catch { /* not ready */ }
      })
    }
    watch(() => props.nodeCount, (n, prev) => {
      if (n > 0 && n !== prev) doFit()
    })
    expose({ fit: doFit })
    return () => null
  },
})

export default defineComponent({
  name: 'WorkflowEditor',
  props: {
    initialWorkflowId: { type: String, default: '' },
  },
  setup(props) {
    const nodes = shallowRef<Node<WfNodeData>[]>([])
    const edges = shallowRef<Edge<WfEdgeData>[]>([])
    const loading = ref(true)
    const canvasLoading = ref(false)
    const selectedWf = ref<any>(null)
    const workflows = ref<any[]>([])
    const running = ref(false)
    const saving = ref(false)
    const runResult = ref('')
    const saveStatus = ref('')
    const showNew = ref(false)
    const newName = ref('')
    const wfInput = ref('')
    const selectedNodeId = ref<string | null>(null)
    const selectedEdgeId = ref<string | null>(null)
    const dirty = ref(false)
    const fitHelperRef = ref<{ fit: () => void } | null>(null)
    const fitCanvas = () => fitHelperRef.value?.fit()

    const selectedNode = () => nodes.value.find(n => n.id === selectedNodeId.value) || null
    const selectedEdge = () => edges.value.find(e => e.id === selectedEdgeId.value) || null

    const nodeLabel = (id: string) => {
      const n = nodes.value.find(x => x.id === id)
      if (!n) return id
      return (n.data as WfNodeData)?.label || id
    }

    const setGraph = (doc: FlowgramDocument) => {
      const g = flowgramToVueFlow(doc)
      nodes.value = g.nodes
      edges.value = g.edges
      dirty.value = false
      if (g.nodes.length) fitCanvas()
    }

    const loadCanvas = async (wfId: string) => {
      canvasLoading.value = true
      selectedNodeId.value = null
      selectedEdgeId.value = null
      try {
        const r = await wailsCall('GetWorkflowFlowgram', wfId)
        if (r?.success && r.flowgram?.nodes?.length) {
          setGraph(r.flowgram)
        } else {
          setGraph(emptyFlowgramDoc())
          dirty.value = true
        }
      } catch {
        setGraph(emptyFlowgramDoc())
        dirty.value = true
      }
      canvasLoading.value = false
    }

    const selectWf = async (wf: any) => {
      if (dirty.value && selectedWf.value && !confirm('当前画布未保存，切换将丢失修改。继续？')) return
      selectedWf.value = wf
      runResult.value = ''
      saveStatus.value = ''
      await loadCanvas(wf.id)
    }

    const saveCanvas = async () => {
      if (!selectedWf.value) return
      saving.value = true
      saveStatus.value = ''
      try {
        const doc = vueFlowToFlowgram(nodes.value, edges.value)
        const r = await wailsCall('SaveWorkflowFlowgram', selectedWf.value.id, JSON.stringify(doc))
        if (r?.success) {
          dirty.value = false
          saveStatus.value = '已保存'
          const list = await wailsCall('ListWorkflows')
          if (Array.isArray(list)) workflows.value = list
        } else {
          saveStatus.value = r?.error || '保存失败'
        }
      } catch (e: any) {
        saveStatus.value = e.message || '保存失败'
      }
      saving.value = false
    }

    const runWf = async () => {
      if (!selectedWf.value) return
      if (dirty.value) await saveCanvas()
      running.value = true
      runResult.value = ''
      try {
        const r = await wailsCall('RunWorkflow', selectedWf.value.id, wfInput.value || '')
        runResult.value = r?.success ? `✓ ${r.output || '运行完成'}` : `✗ ${r?.error || '失败'}`
      } catch (e: any) {
        runResult.value = `✗ ${e.message}`
      }
      running.value = false
    }

    const createWf = async () => {
      const name = newName.value.trim()
      if (!name) return
      try {
        const id = `wf-${Date.now()}`
        await wailsCall('SaveWorkflow', { id, name, description: '', nodes: [], edges: [] })
        const doc = emptyFlowgramDoc()
        await wailsCall('SaveWorkflowFlowgram', id, JSON.stringify(doc))
        workflows.value = (await wailsCall('ListWorkflows')) || []
        showNew.value = false
        newName.value = ''
        const wf = workflows.value.find((w: any) => w.id === id) || { id, name }
        selectedWf.value = wf
        setGraph(doc)
      } catch (e: any) {
        alert('创建失败: ' + e.message)
      }
    }

    const ensureHappyPath = async () => {
      try {
        const r = await wailsCall('EnsureWorkflowHappyPath')
        if (!r?.success) {
          saveStatus.value = r?.error || '加载演示失败'
          return
        }
        workflows.value = (await wailsCall('ListWorkflows')) || []
        const wf = workflows.value.find((w: any) => w.id === (r.id || 'wf_happy_path'))
        if (wf) await selectWf(wf)
        saveStatus.value = r.message || '演示工作流已就绪'
      } catch (e: any) {
        saveStatus.value = e.message
      }
    }

    const onNodesChange = (changes: NodeChange[]) => {
      nodes.value = applyNodeChanges(changes, nodes.value as any) as Node<WfNodeData>[]
      if (changes.some(c => c.type === 'position' || c.type === 'remove')) dirty.value = true
    }

    const onEdgesChange = (changes: EdgeChange[]) => {
      edges.value = applyEdgeChanges(changes, edges.value as any) as Edge<WfEdgeData>[]
      if (changes.some(c => c.type === 'remove')) dirty.value = true
    }

    const onConnect = (conn: Connection) => {
      if (!conn.source || !conn.target) return
      if (conn.source === conn.target) return
      const srcNode = nodes.value.find(n => n.id === conn.source)
      const tgtNode = nodes.value.find(n => n.id === conn.target)
      const srcFt = (srcNode?.data as WfNodeData)?.flowType || ''
      const tgtFt = (tgtNode?.data as WfNodeData)?.flowType || ''
      const check = validateConnection(conn, srcFt, tgtFt)
      if (!check.ok) {
        saveStatus.value = check.reason || '连接被拒绝'
        return
      }
      const sh = conn.sourceHandle ?? ''
      const exists = edges.value.some(e =>
        e.source === conn.source &&
        e.target === conn.target &&
        (e.sourceHandle ?? '') === sh,
      )
      if (exists) return
      let when = whenFromSourceHandle(srcFt, conn.sourceHandle)
      if (isBranchFlowType(srcFt) && srcNode?.data) {
        const d = srcNode.data as WfNodeData
        const table = attachTargetToBranchRow(
          d.fields?.branch_table || '',
          conn.sourceHandle,
          conn.target,
          when,
        )
        if (!d.fields) d.fields = {}
        d.fields.branch_table = table
        const row = parseBranchTable(table).find(r => r.target === conn.target || r.id === (conn.sourceHandle || ''))
        if (row) when = row.when
      }
      const condition = parseCondition(when)
      const edgeId = `e-${conn.source}-${conn.target}-${Date.now()}`
      edges.value = [
        ...edges.value,
        {
          id: edgeId,
          source: conn.source,
          target: conn.target,
          sourceHandle: conn.sourceHandle ?? undefined,
          targetHandle: conn.targetHandle ?? 'in',
          type: 'smoothstep',
          animated: true,
          label: when || undefined,
          labelStyle: { fill: 'var(--text-strong)', fontSize: 11, fontWeight: 600 },
          labelBgStyle: { fill: 'var(--surface)', fillOpacity: 0.92 },
          labelBgPadding: [4, 8] as [number, number],
          labelBgBorderRadius: 4,
          data: { when, condition },
          style: { stroke: 'var(--accent)', strokeWidth: 2 },
        },
      ]
      selectedEdgeId.value = edgeId
      selectedNodeId.value = null
      dirty.value = true
    }

    const onNodeClick = ({ node }: { node: Node }) => {
      selectedNodeId.value = node.id
      selectedEdgeId.value = null
    }

    const onEdgeClick = ({ edge }: { edge: Edge }) => {
      selectedEdgeId.value = edge.id
      selectedNodeId.value = null
    }

    const onPaneClick = () => {
      selectedNodeId.value = null
      selectedEdgeId.value = null
    }

    const updateEdgeWhen = (when: string) => {
      const id = selectedEdgeId.value
      if (!id) return
      const w = when.trim()
      const condition = parseCondition(w)
      const edge = edges.value.find(e => e.id === id)
      edges.value = edges.value.map(e => {
        if (e.id !== id) return e
        return { ...e, data: { when: w, condition }, label: w || undefined }
      })
      if (edge) {
        const srcNode = nodes.value.find(n => n.id === edge.source)
        const ft = (srcNode?.data as WfNodeData)?.flowType || ''
        if (srcNode?.data && isBranchFlowType(ft)) {
          const d = srcNode.data as WfNodeData
          if (!d.fields) d.fields = {}
          d.fields.branch_table = syncBranchTableFromEdge(
            edge.source,
            { ...edge, data: { when: w, condition } },
            d.fields.branch_table || '',
          )
        }
      }
      dirty.value = true
    }

    const syncEdgesFromBranchNode = () => {
      const node = selectedNode()
      if (!node?.data) return
      const ft = (node.data as WfNodeData).flowType
      if (!isBranchFlowType(ft)) return
      const table = parseBranchTable((node.data as WfNodeData).fields?.branch_table)
      edges.value = syncEdgesFromBranchTable(node.id, table, edges.value)
      dirty.value = true
    }

    const deleteSelectedEdge = () => {
      const id = selectedEdgeId.value
      if (!id) return
      edges.value = edges.value.filter(e => e.id !== id)
      selectedEdgeId.value = null
      dirty.value = true
    }

    const addNode = (type: string) => {
      if (!selectedWf.value) return
      const baseX = 120 + (nodes.value.length % 4) * 220
      const baseY = 80 + Math.floor(nodes.value.length / 4) * 130
      const fn = createFlowgramNode(type, { x: baseX, y: baseY })
      nodes.value = [...nodes.value, flowgramNodeToVue(fn)]
      selectedNodeId.value = fn.id
      dirty.value = true
    }

    const deleteSelectedNode = () => {
      const id = selectedNodeId.value
      if (!id) return
      const n = nodes.value.find(x => x.id === id)
      const ft = (n?.data as WfNodeData)?.flowType
      if (ft === 'Start') return
      nodes.value = nodes.value.filter(x => x.id !== id)
      edges.value = edges.value.filter(e => e.source !== id && e.target !== id)
      selectedNodeId.value = null
      dirty.value = true
    }

    onMounted(async () => {
      try {
        workflows.value = (await wailsCall('ListWorkflows')) || []
      } catch { /* preview */ }
      loading.value = false
      const initialID = props.initialWorkflowId.trim()
      const initialWf = initialID
        ? (workflows.value.find((wf: any) => wf.id === initialID) || { id: initialID, name: initialID })
        : workflows.value[0]
      if (initialWf) {
        if (initialID && !workflows.value.some((wf: any) => wf.id === initialID)) {
          workflows.value = [initialWf, ...workflows.value]
        }
        await selectWf(initialWf)
      }
    })

    const btnStyle = (primary = false) => ({
      padding: '3px 12px',
      border: primary ? 'none' : '1px solid var(--border)',
      borderRadius: 'var(--radius-md)',
      background: primary ? 'var(--accent-bg)' : 'transparent',
      color: primary ? 'var(--accent-text)' : 'var(--text-dim)',
      fontSize: '12px',
      cursor: 'pointer',
      fontWeight: primary ? 600 : 400,
    })

    return () => (
      <div class="wf-editor">
        <div class="wf-toolbar">
          <div class="wf-toolbar-row wf-toolbar-row--main">
            <span class="wf-toolbar-title">⚡ Workflow</span>
            <div class="wf-toolbar-actions">
            {showNew.value ? (
              <>
                <input
                  class="wf-name-input"
                  value={newName.value}
                  onInput={(e: Event) => { newName.value = (e.target as HTMLInputElement).value }}
                  onKeydown={(e: KeyboardEvent) => {
                    if (e.key === 'Enter') createWf()
                    if (e.key === 'Escape') showNew.value = false
                  }}
                  placeholder="名称…"
                />
                <button type="button" style={btnStyle(true)} onClick={createWf}>创建</button>
                <button type="button" style={btnStyle()} onClick={() => { showNew.value = false }}>取消</button>
              </>
            ) : (
              <>
                <button type="button" style={btnStyle()} onClick={() => { showNew.value = true }}>+ 新建</button>
                <button type="button" style={btnStyle()} onClick={ensureHappyPath}>演示模板</button>
                {selectedWf.value && (
                  <>
                    <button type="button" style={btnStyle()} disabled={saving.value} onClick={saveCanvas}>
                      {saving.value ? '保存中…' : dirty.value ? '● 保存' : '保存'}
                    </button>
                    <input
                      class="wf-run-input"
                      value={wfInput.value}
                      onInput={(e: Event) => { wfInput.value = (e.target as HTMLInputElement).value }}
                      placeholder="运行输入（可选）"
                    />
                    <button type="button" style={btnStyle(true)} disabled={running.value} onClick={runWf}>
                      {running.value ? '运行中…' : '▶ 运行'}
                    </button>
                  </>
                )}
              </>
            )}
            </div>
          </div>
          <div class="wf-toolbar-row wf-toolbar-row--tabs">
            <div class="wf-wf-tabs">
              {workflows.value.map((wf: any) => (
                <button
                  key={wf.id}
                  type="button"
                  class={['wf-tab', selectedWf.value?.id === wf.id && 'active']}
                  title={wf.name || wf.id}
                  onClick={() => selectWf(wf)}
                >{wf.name || wf.id}</button>
              ))}
              {!workflows.value.length && !loading.value && (
                <span class="wf-muted">暂无 Workflow</span>
              )}
            </div>
          </div>
        </div>

        {(runResult.value || saveStatus.value) && (
          <div class={`wf-status ${runResult.value.startsWith('✓') ? 'ok' : runResult.value.startsWith('✗') ? 'err' : ''}`}>
            {[saveStatus.value, runResult.value].filter(Boolean).join(' · ')}
          </div>
        )}

        <div class="wf-body">
          <WorkflowNodePalette onAdd={addNode} />

          <div class="wf-canvas-wrap">
            {loading.value || canvasLoading.value ? (
              <div class="wf-loading">加载画布…</div>
            ) : (
              <VueFlow
                nodes={nodes.value}
                edges={edges.value}
                nodeTypes={nodeTypes}
                onNodesChange={onNodesChange}
                onEdgesChange={onEdgesChange}
                onConnect={onConnect}
                onNodeClick={onNodeClick}
                onEdgeClick={onEdgeClick}
                onPaneClick={onPaneClick}
                fitViewOnInit
                nodesConnectable
                edgesUpdatable
                deleteKeyCode={['Backspace', 'Delete']}
                connectionLineStyle={{ stroke: 'var(--accent)', strokeWidth: 2 }}
                defaultEdgeOptions={{
                  type: 'smoothstep',
                  animated: true,
                  style: { stroke: 'var(--accent)', strokeWidth: 2 },
                }}
                class="wf-vue-flow"
              >
                <Background gap={16} size={1} color="var(--border-subtle)" />
                <Controls showInteractive={false} />
                <FitViewHelper ref={fitHelperRef} nodeCount={nodes.value.length} />
              </VueFlow>
            )}
          </div>

          {selectedEdgeId.value ? (
            <WorkflowEdgeProps
              edge={selectedEdge()}
              nodes={nodes.value}
              edges={edges.value}
              sourceLabel={nodeLabel(selectedEdge()?.source || '')}
              targetLabel={nodeLabel(selectedEdge()?.target || '')}
              onUpdate={updateEdgeWhen}
              onDelete={deleteSelectedEdge}
            />
          ) : (
            <FormMetaPanel
              node={selectedNode()}
              nodes={nodes.value}
              edges={edges.value}
              onUpdate={() => { dirty.value = true }}
              onSyncEdges={syncEdgesFromBranchNode}
              onDelete={deleteSelectedNode}
            />
          )}
        </div>

        <div class="wf-footer">
          <span class="wf-footer-hint">
            {nodes.value.length
              ? `${nodes.value.length} 节点 · ${edges.value.length} 连线 · 点击节点/连线配置`
              : '从左侧添加节点，或点「演示模板」加载示例'}
          </span>
          {nodes.value.length > 0 && (
            <button type="button" class="wf-link-btn" onClick={fitCanvas}>适应画布</button>
          )}
        </div>
      </div>
    )
  },
})
