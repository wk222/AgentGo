import { defineComponent, ref, shallowRef, onMounted, watch, nextTick, markRaw, type PropType } from 'vue'
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
import {
  Plus, Sparkles, Save, Play, LayoutGrid, Maximize2,
  CheckCircle2, AlertCircle, Loader2, X, ListTodo, ExternalLink,
} from 'lucide-vue-next'
import { stageState } from '../../stores/agentBridge'
import { wailsCall } from '../../shared/wails'
import FormMetaPanel from './form/FormMetaPanel'
import WorkflowEdgeProps from './WorkflowEdgeProps'
import WorkflowNodePalette from './WorkflowNodePalette'
import WorkflowFlowNode from './WorkflowFlowNode'
import { isBranchFlowType, whenFromSourceHandle } from './model/branchUtils'
import {
  attachTargetToBranchRow,
  syncBranchTableFromEdge,
  syncEdgesFromBranchTable,
  parseBranchTable,
} from './model/branchTable'
import { validateConnection } from './model/portTypes'
import { parseCondition } from './model/conditionModel'
import {
  flowgramToVueFlow,
  vueFlowToFlowgram,
  emptyFlowgramDoc,
  createFlowgramNode,
  flowgramNodeToVue,
  autoLayoutVueNodes,
  WF_NODE_TYPE,
  type WfNodeData,
  type WfEdgeData,
  type FlowgramDocument,
} from './model/flowgramAdapter'
import { normalizeCatalogType } from './model/nodeCatalog'
import { notify, confirmDialog } from '../../shared/ui/notify'

const nodeTypes = markRaw({ [WF_NODE_TYPE]: WorkflowFlowNode })

/** 须在 VueFlow 子树内调用 useVueFlow */
const FitViewHelper = defineComponent({
  name: 'FitViewHelper',
  props: {
    nodeCount: { type: Number, required: true },
  },
  setup(props, { expose }) {
    const { fitView, project } = useVueFlow()
    const doFit = () => {
      nextTick(() => {
        try {
          fitView({ padding: 0.22, duration: 220 })
        } catch { /* not ready */ }
      })
    }
    watch(() => props.nodeCount, (n, prev) => {
      if (n > 0 && n !== prev) doFit()
    })
    expose({ fit: doFit, project })
    return () => null
  },
})

export default defineComponent({
  name: 'WorkflowEditor',
  props: {
    initialWorkflowId: { type: String, default: '' },
    embedded: {type:Boolean,default:false},
    listCount: { type: Number, default: -1 },
    onShowList: { type: Function as PropType<() => void>, default: undefined },
    onPopout: { type: Function as PropType<() => void>, default: undefined },
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
    const runOutput = ref('')
    const runError = ref('')
    const runDurationMs = ref(0)
    const saveStatus = ref('')
    const showNew = ref(false)
    const newName = ref('')
    const wfInput = ref('请帮我梳理当前项目的关键模块，并输出结构化摘要。')
    const selectedNodeId = ref<string | null>(null)
    const selectedEdgeId = ref<string | null>(null)
    const showPalette = ref(!props.embedded)
    const showTestDrawer = ref(false)
    const dirty = ref(false)
    watch([selectedWf, selectedNodeId, dirty], ([wf,nodeId,unsaved]) => {
      stageState.workflowSelection=wf ? {kind:'workflow',id:String(wf.id),label:String(wf.name || wf.id),nodeId:nodeId || undefined,version:String(wf.updated_at || wf.version || ''),dirty:unsaved} : null
    },{immediate:true})
    const canvasWrapRef = ref<HTMLElement | null>(null)
    const fitHelperRef = ref<{ fit: () => void; project: (p: { x: number; y: number }) => { x: number; y: number } } | null>(null)

    const fitCanvas = () => fitHelperRef.value?.fit()

    const selectedNode = () => nodes.value.find(n => n.id === selectedNodeId.value) || null
    const selectedEdge = () => edges.value.find(e => e.id === selectedEdgeId.value) || null

    const nodeLabel = (id: string) => {
      const n = nodes.value.find(x => x.id === id)
      if (!n) return id
      return (n.data as WfNodeData)?.label || id
    }

    const setAllNodesRunStatus = (status: WfNodeData['runStatus']) => {
      nodes.value = nodes.value.map(n => ({
        ...n,
        data: {
          ...(n.data as WfNodeData),
          runStatus: status,
        },
      }))
    }

    const setGraph = (doc: FlowgramDocument) => {
      const g = flowgramToVueFlow(doc)
      nodes.value = g.nodes
      edges.value = g.edges
      dirty.value = false
      if (g.nodes.length) fitCanvas()
    }

    const autoLayoutCanvas = () => {
      nodes.value = autoLayoutVueNodes(nodes.value, edges.value)
      dirty.value = true
      saveStatus.value = '已自动对齐排版'
      fitCanvas()
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
      if (dirty.value && selectedWf.value && selectedWf.value.id !== wf.id) {
        const ok = await confirmDialog('当前画布未保存，切换将丢失修改。', { title: '放弃未保存的修改', okText: '放弃并切换', danger: true })
        if (!ok) return
      }
      selectedWf.value = wf
      runOutput.value = ''
      runError.value = ''
      saveStatus.value = ''
      await loadCanvas(wf.id)
    }

    const saveCanvas = async () => {
      if (!selectedWf.value) return false
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
          return true
        } else {
          saveStatus.value = r?.error || '保存失败'
          return false
        }
      } catch (e: any) {
        saveStatus.value = e.message || '保存失败'
        return false
      } finally {
        saving.value = false
      }
    }

    const openTestRunDrawer = () => {
      selectedNodeId.value = null
      selectedEdgeId.value = null
      showTestDrawer.value = true
    }

    const runWf = async () => {
      showTestDrawer.value = true
      if (!selectedWf.value) return
      if (dirty.value) await saveCanvas()
      running.value = true
      runOutput.value = ''
      runError.value = ''
      const t0 = performance.now()
      setAllNodesRunStatus('running')
      try {
        const r = await wailsCall('RunWorkflow', selectedWf.value.id, wfInput.value || '')
        runDurationMs.value = Math.round(performance.now() - t0)
        if (r?.success) {
          runOutput.value = r.output || '执行完成'
          setAllNodesRunStatus('success')
        } else {
          runError.value = r?.error || '运行失败'
          setAllNodesRunStatus('error')
        }
      } catch (e: any) {
        runDurationMs.value = Math.round(performance.now() - t0)
        runError.value = e.message || '执行异常'
        setAllNodesRunStatus('error')
      } finally {
        running.value = false
      }
    }

    const createWf = async () => {
      const name = newName.value.trim() || `工作流 ${workflows.value.length + 1}`
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
        saveStatus.value = '已创建新工作流'
      } catch (e: any) {
        notify.error('创建失败: ' + e.message)
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

    const makeStyledEdge = (source: string, target: string, sourceHandle?: string, when = ''): Edge<WfEdgeData> => {
      const condition = parseCondition(when)
      return {
        id: `e-${source}-${target}-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
        source,
        target,
        sourceHandle: sourceHandle || undefined,
        targetHandle: 'in',
        type: 'smoothstep',
        animated: true,
        label: when || undefined,
        labelStyle: { fill: 'var(--text-strong)', fontSize: 11, fontWeight: 600 },
        labelBgStyle: { fill: 'var(--surface)', fillOpacity: 0.92 },
        labelBgPadding: [4, 8] as [number, number],
        labelBgBorderRadius: 6,
        data: { when, condition },
        style: { stroke: '#4e40e5', strokeWidth: 2 },
      }
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
      const newEdge = makeStyledEdge(conn.source, conn.target, conn.sourceHandle ?? undefined, when)
      edges.value = [...edges.value, newEdge]
      selectedEdgeId.value = newEdge.id
      selectedNodeId.value = null
      showTestDrawer.value = false
      dirty.value = true
    }

    const onNodeClick = ({ node }: { node: Node }) => {
      selectedNodeId.value = node.id
      selectedEdgeId.value = null
      showTestDrawer.value = false
    }

    const onEdgeClick = ({ edge }: { edge: Edge }) => {
      selectedEdgeId.value = edge.id
      selectedNodeId.value = null
      showTestDrawer.value = false
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

    const addNode = (rawType: string, dropPosition?: { x: number; y: number }) => {
      const type = normalizeCatalogType(rawType)
      const sel = selectedNode()
      const endNode = nodes.value.find(n => (n.data as WfNodeData)?.flowType === 'End')
      const startNode = nodes.value.find(n => (n.data as WfNodeData)?.flowType === 'Start')

      let pos = dropPosition
      let autoSourceId: string | null = null
      let autoTargetEndId: string | null = null

      if (!pos) {
        if (sel && (sel.data as WfNodeData)?.flowType !== 'End') {
          autoSourceId = sel.id
          pos = { x: (sel.position?.x ?? 100) + 340, y: sel.position?.y ?? 160 }
        } else if (endNode) {
          const edgeIntoEnd = edges.value.find(e => e.target === endNode.id && !e.sourceHandle)
          if (edgeIntoEnd) {
            autoSourceId = edgeIntoEnd.source
            autoTargetEndId = endNode.id
            const srcNode = nodes.value.find(n => n.id === autoSourceId)
            pos = { x: (srcNode?.position?.x ?? 100) + 340, y: srcNode?.position?.y ?? 160 }
          } else if (startNode) {
            autoSourceId = startNode.id
            pos = { x: (startNode.position?.x ?? 80) + 340, y: startNode.position?.y ?? 160 }
          }
        }
        if (!pos) {
          pos = {
            x: 120 + (nodes.value.length % 4) * 320,
            y: 120 + Math.floor(nodes.value.length / 4) * 180,
          }
        }
      }

      const fn = createFlowgramNode(type, pos)
      const vueNode = flowgramNodeToVue(fn)
      let nextEdges = [...edges.value]

      if (autoSourceId && type !== 'Start') {
        if (autoTargetEndId) {
          nextEdges = nextEdges.filter(e => !(e.source === autoSourceId && e.target === autoTargetEndId))
          nextEdges.push(
            makeStyledEdge(autoSourceId, fn.id),
            makeStyledEdge(fn.id, autoTargetEndId),
          )
        } else {
          nextEdges.push(makeStyledEdge(autoSourceId, fn.id))
        }
      }

      const nextNodes = [...nodes.value, vueNode]
      nodes.value = dropPosition ? nextNodes : autoLayoutVueNodes(nextNodes, nextEdges)
      edges.value = nextEdges
      selectedNodeId.value = fn.id
      selectedEdgeId.value = null
      showTestDrawer.value = false
      dirty.value = true
      if (!dropPosition) fitCanvas()
    }

    const onCanvasDragOver = (e: DragEvent) => {
      e.preventDefault()
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy'
    }

    const onCanvasDrop = (e: DragEvent) => {
      e.preventDefault()
      const rawType = e.dataTransfer?.getData('application/agentgo-wf-node')
      if (!rawType) return
      const wrap = canvasWrapRef.value
      const proj = fitHelperRef.value?.project
      if (wrap && proj) {
        const rect = wrap.getBoundingClientRect()
        const p = proj({
          x: e.clientX - rect.left - 120,
          y: e.clientY - rect.top - 40,
        })
        addNode(rawType, { x: Math.round(p.x), y: Math.round(p.y) })
      } else {
        addNode(rawType)
      }
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
      } else {
        setGraph(emptyFlowgramDoc())
      }
    })

    return () => (
      <div class={['wf-editor','coze-wf-studio',props.embedded && 'is-embedded-workflow']}>
        {/* Single Unified Coze Studio Topbar */}
        <header class="coze-studio-topbar">
          <div class="coze-topbar-left">
            <span class="coze-brand-badge">
              <span class="coze-brand-dot" />
              <span>工作流</span>
            </span>

            {workflows.value.length > 0 && (
              <select
                class="coze-wf-select"
                value={selectedWf.value?.id || ''}
                title="切换工作流"
                onChange={(e: Event) => {
                  const targetId = (e.target as HTMLSelectElement).value
                  const found = workflows.value.find((w: any) => w.id === targetId)
                  if (found) void selectWf(found)
                }}
              >
                {workflows.value.map((wf: any) => (
                  <option key={wf.id} value={wf.id}>
                    {wf.name || wf.id}
                  </option>
                ))}
              </select>
            )}

            {showNew.value ? (
              <div style={{ display: 'inline-flex', alignItems: 'center', gap: '6px' }}>
                <input
                  class="coze-wf-name-input"
                  style={{ background: 'var(--input-bg)', borderColor: '#4e40e5', width: '150px' }}
                  value={newName.value}
                  onInput={(e: Event) => { newName.value = (e.target as HTMLInputElement).value }}
                  onKeydown={(e: KeyboardEvent) => {
                    if (e.key === 'Enter') void createWf()
                    if (e.key === 'Escape') showNew.value = false
                  }}
                  placeholder="输入新工作流名称…"
                />
                <button type="button" class="coze-top-btn primary" onClick={createWf}>确定</button>
                <button type="button" class="coze-top-btn" onClick={() => { showNew.value = false }}>取消</button>
              </div>
            ) : (
              <span class={['coze-save-status', dirty.value ? 'is-dirty' : 'is-clean']}>
                {dirty.value ? '● 未保存修改' : (saveStatus.value || '✓ 已就绪')}
              </span>
            )}
          </div>

          <div class="coze-topbar-right">
            {!showNew.value && (
              <button
                type="button"
                class="coze-top-btn"
                onClick={() => { showNew.value = true }}
                title="新建工作流"
              >
                <Plus size={13} />
                <span>新建</span>
              </button>
            )}

            <button
              type="button"
              class="coze-top-btn"
              onClick={ensureHappyPath}
              title="加载多步协作模板"
            >
              <Sparkles size={13} />
              <span>演示模板</span>
            </button>

            {selectedWf.value && (
              <>
                <button
                  type="button"
                  class="coze-top-btn"
                  disabled={saving.value}
                  onClick={saveCanvas}
                >
                  <Save size={13} />
                  <span>{saving.value ? '保存中…' : dirty.value ? '● 保存' : '保存'}</span>
                </button>
                <button
                  type="button"
                  class="coze-top-btn primary"
                  disabled={running.value}
                  onClick={openTestRunDrawer}
                >
                  <Play size={13} />
                  <span>{running.value ? '运行中…' : '试运行'}</span>
                </button>
              </>
            )}

            {props.onShowList && (
              <button
                type="button"
                class="coze-top-btn"
                title="查看工作流资产列表与权限治理"
                onClick={props.onShowList}
              >
                <ListTodo size={13} />
                <span>列表与授权{props.listCount >= 0 ? ` (${props.listCount})` : ''}</span>
              </button>
            )}

            {props.onPopout && (
              <button
                type="button"
                class="coze-top-btn icon-only"
                title="在独立窗口中打开工作流画布"
                onClick={props.onPopout}
              >
                <ExternalLink size={13} />
              </button>
            )}
          </div>
        </header>

        {/* Main Studio Body: Left Searchable Node Library + Center Infinite Canvas + Contextual Right Drawer */}
        <div class="wf-body coze-studio-body">
          {showPalette.value && (
            <WorkflowNodePalette
              onAdd={(type: string) => addNode(type)}
              onClose={() => { showPalette.value = false }}
            />
          )}

          <div
            ref={canvasWrapRef}
            class="wf-canvas-wrap coze-canvas-wrap"
            onDragover={onCanvasDragOver}
            onDrop={onCanvasDrop}
          >
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
                minZoom={0.25}
                maxZoom={1.75}
                nodesConnectable
                edgesUpdatable
                deleteKeyCode={['Backspace', 'Delete']}
                connectionLineStyle={{ stroke: '#4e40e5', strokeWidth: 2 }}
                defaultEdgeOptions={{
                  type: 'smoothstep',
                  animated: true,
                  style: { stroke: '#4e40e5', strokeWidth: 2 },
                }}
                class="wf-vue-flow"
              >
                <Background gap={20} size={1.2} patternColor="#cbd5e1" />
                <Controls position="bottom-left" showInteractive={false} />
                <FitViewHelper ref={fitHelperRef} nodeCount={nodes.value.length} />
              </VueFlow>
            )}

            {/* Coze Studio Signature Floating Bottom Control Dock */}
            <div class="coze-canvas-dock">
              <button
                type="button"
                class={['coze-dock-btn', showPalette.value && 'active']}
                onClick={() => { showPalette.value = !showPalette.value }}
                title={showPalette.value ? '收起左侧节点库' : '展开左侧节点库'}
              >
                <Plus size={14} />
                <span>添加节点</span>
              </button>

              <span class="coze-dock-sep" />

              <button
                type="button"
                class="coze-dock-btn"
                onClick={autoLayoutCanvas}
                title="按 DAG 拓扑层级自动横向对齐排版"
              >
                <LayoutGrid size={14} />
                <span>自动排版</span>
              </button>

              <button
                type="button"
                class="coze-dock-btn"
                onClick={fitCanvas}
                title="缩放画布以适应全部节点"
              >
                <Maximize2 size={14} />
                <span>适应画布</span>
              </button>

              <span class="coze-dock-sep" />

              <button
                type="button"
                class="coze-dock-btn run"
                disabled={running.value}
                onClick={runWf}
                title="立即保存并试运行当前工作流"
              >
                <Play size={13} />
                <span>{running.value ? '运行中…' : '试运行'}</span>
              </button>

              <span class="coze-dock-stats">
                {nodes.value.length} 节点 · {edges.value.length} 连线
              </span>
            </div>
          </div>

          {/* Contextual Right Drawer: Test Run OR Edge Inspector OR Node Inspector */}
          {showTestDrawer.value ? (
            <aside class="wf-node-props coze-inspector-drawer coze-testrun-drawer">
              <div class="coze-inspector-header">
                <div class="coze-inspector-head-top">
                  <span class="coze-inspector-icon" style={{ background: '#10b981' }}>▶</span>
                  <span class="coze-inspector-edge-title">试运行调试台 (Test Run)</span>
                  <button
                    type="button"
                    class="coze-inspector-icon-btn"
                    title="关闭试运行面板"
                    onClick={() => { showTestDrawer.value = false }}
                  >
                    <X size={15} />
                  </button>
                </div>
                <p class="coze-inspector-desc">
                  输入测试参数（传入 <code>{'{{input}}'}</code>），实时验证 Eino Graph 全链路执行输出。
                </p>
              </div>

              <div class="coze-inspector-scroll">
                <div class="coze-inspector-section">
                  <div class="coze-inspector-sec-head">
                    <span class="coze-inspector-sec-title">测试输入 (start.input)</span>
                  </div>
                  <textarea
                    class="coze-testrun-textarea"
                    rows={4}
                    value={wfInput.value}
                    placeholder="输入测试文本或 JSON 参数..."
                    onInput={(e: Event) => { wfInput.value = (e.target as HTMLTextAreaElement).value }}
                  />
                  <button
                    type="button"
                    class="coze-testrun-exec-btn"
                    disabled={running.value}
                    onClick={runWf}
                  >
                    {running.value ? <Loader2 size={14} class="spin" /> : <Play size={14} />}
                    <span>{running.value ? '正在执行工作流…' : '开始试运行'}</span>
                  </button>
                </div>

                {(runOutput.value || runError.value || running.value) && (
                  <div class="coze-inspector-section">
                    <div class="coze-inspector-sec-head">
                      <span class="coze-inspector-sec-title">
                        {running.value ? (
                          <span class="coze-run-state running"><Loader2 size={12} class="spin" /> 执行中…</span>
                        ) : runError.value ? (
                          <span class="coze-run-state error"><AlertCircle size={12} /> 执行失败</span>
                        ) : (
                          <span class="coze-run-state success"><CheckCircle2 size={12} /> 执行成功</span>
                        )}
                      </span>
                      {runDurationMs.value > 0 && (
                        <span class="coze-inspector-sec-sub">{runDurationMs.value} ms</span>
                      )}
                    </div>
                    <pre class={['coze-testrun-output', runError.value && 'is-error']}>
                      {running.value ? '正在调度节点执行，请稍候…' : (runError.value || runOutput.value)}
                    </pre>
                  </div>
                )}
              </div>
            </aside>
          ) : selectedEdgeId.value ? (
            <WorkflowEdgeProps
              edge={selectedEdge()}
              nodes={nodes.value}
              edges={edges.value}
              sourceLabel={nodeLabel(selectedEdge()?.source || '')}
              targetLabel={nodeLabel(selectedEdge()?.target || '')}
              onUpdate={updateEdgeWhen}
              onDelete={deleteSelectedEdge}
              onClose={() => { selectedEdgeId.value = null }}
            />
          ) : selectedNodeId.value ? (
            <FormMetaPanel
              node={selectedNode()}
              nodes={nodes.value}
              edges={edges.value}
              onUpdate={() => {
                nodes.value = [...nodes.value]
                dirty.value = true
              }}
              onSyncEdges={syncEdgesFromBranchNode}
              onDelete={deleteSelectedNode}
              onClose={() => { selectedNodeId.value = null }}
            />
          ) : null}
        </div>
      </div>
    )
  },
})
