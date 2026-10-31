import { defineComponent, ref, onMounted } from 'vue'
import { wailsCall } from '../../shared/wails'
import WorkflowEditorComp from './WorkflowEditor'
import { emptyFlowgramDoc } from './model/flowgramAdapter'
import { notify } from '../../shared/ui/notify'
import { panelBodyStyle, scrollStyle, EmptyState, PanelHeader, miniButtonStyle, rowDiv } from '../../shared/ui/panelKit'

export const WorkflowPanel = defineComponent({
  name: 'WorkflowPanel',
  props: {
    initialWorkflowId: { type: String, default: '' },
    standalone: { type: Boolean, default: false },
  },
  setup(props) {
    if (props.standalone) {
      return () => (
        <div class="wf-panel-host">
          <WorkflowEditorComp initialWorkflowId={props.initialWorkflowId} />
        </div>
      )
    }

    const workflows = ref<any[]>([])
    const grants = ref<any[]>([])
    const loading = ref(true)
    const showNew = ref(false)
    const newName = ref('')
    const newDesc = ref('')
    const statusMsg = ref('')
    const viewMode = ref<'canvas' | 'list'>('canvas')
    const activeWfId = ref<string>(props.initialWorkflowId || '')

    const loadWorkflows = async () => {
      loading.value = true
      try {
        workflows.value = (await wailsCall('ListWorkflows')) || []
        grants.value = (await wailsCall('ListCapabilityGrants', 'workflow')) || []
      } catch (e) {
        console.error('[AgentGo] loadWorkflows:', e)
      }
      loading.value = false
    }

    const getWorkflowGrant = (wfId: string) => {
      const grantId = `workflow:${wfId}:global`
      return grants.value.find(g => g.id === grantId)
    }

    const isWorkflowEnabled = (wfId: string) => {
      const g = getWorkflowGrant(wfId)
      return g ? g.status === 'published' : true // default to enabled if not found
    }

    const toggleWorkflowStatus = async (wfId: string) => {
      const currentlyEnabled = isWorkflowEnabled(wfId)
      const targetStatus = currentlyEnabled ? 'retired' : 'published'
      const grantId = `workflow:${wfId}:global`
      try {
        await wailsCall('TransitionCapabilityGrant', grantId, targetStatus)
        // Refresh grants
        grants.value = (await wailsCall('ListCapabilityGrants', 'workflow')) || []
      } catch (e: any) {
        notify.error('修改状态失败: ' + (e.message || e))
      }
    }

    const openWfEditor = async (wfId: string) => {
      try {
        await wailsCall('OpenWorkflowWindow', wfId)
      } catch (e: any) {
        notify.error('无法打开独立窗口: ' + (e.message || e))
      }
    }

    const createWf = async () => {
      const name = newName.value.trim()
      if (!name) return
      try {
        const id = `wf-${Date.now()}`
        await wailsCall('SaveWorkflow', { id, name, description: newDesc.value.trim(), nodes: [], edges: [] })
        
        // Save standard Coze-style initial flowgram document
        const emptyDoc = emptyFlowgramDoc()
        await wailsCall('SaveWorkflowFlowgram', id, JSON.stringify(emptyDoc))
        
        showNew.value = false
        newName.value = ''
        newDesc.value = ''
        
        // Reload list & open inline
        await loadWorkflows()
        openInInlineCanvas(id)
      } catch (e: any) {
        notify.error('创建失败: ' + (e.message || e))
      }
    }

    const loadDemoTemplate = async () => {
      statusMsg.value = '正在加载演示模板...'
      try {
        const r = await wailsCall('EnsureWorkflowHappyPath')
        if (r?.success) {
          statusMsg.value = r.message || '演示工作流加载成功'
          await loadWorkflows()
        } else {
          statusMsg.value = r?.error || '加载失败'
        }
      } catch (e: any) {
        statusMsg.value = e.message || '加载失败'
      }
      setTimeout(() => { statusMsg.value = '' }, 4000)
    }

    const openInInlineCanvas = (wfId: string) => {
      activeWfId.value = wfId
      viewMode.value = 'canvas'
    }

    onMounted(loadWorkflows)

    return () => (
      <div class="wf-studio-shell">
        {viewMode.value === 'canvas' ? (
          <div class="wf-studio-canvas-slot">
            <WorkflowEditorComp
              embedded
              key={activeWfId.value || 'default'}
              initialWorkflowId={activeWfId.value}
              listCount={workflows.value.length}
              onShowList={() => { viewMode.value = 'list'; void loadWorkflows() }}
              onPopout={() => openWfEditor(activeWfId.value || workflows.value[0]?.id || '')}
            />
          </div>
        ) : (
          <>
            <div class="wf-studio-Topbar">
              <div class="wf-studio-brand">
                <span class="wf-studio-badge">工作流</span>
                <span class="wf-studio-title">我的工作流</span>
              </div>
              <div class="wf-studio-actions">
                <button
                  type="button"
                  class="wf-studio-seg-btn active"
                  onClick={() => { viewMode.value = 'canvas' }}
                >
                  ← 返回画布
                </button>
                <button
                  type="button"
                  class="wf-studio-popout-btn"
                  onClick={() => openWfEditor(activeWfId.value || workflows.value[0]?.id || '')}
                  title="在独立桌面窗口中打开工作流画布"
                >
                  ↗ 独立窗口
                </button>
              </div>
            </div>
          <div style={scrollStyle}>
            {statusMsg.value && (
              <div style={{ color: 'var(--accent)', fontSize: '12px', marginBottom: '10px' }}>{statusMsg.value}</div>
            )}

            {/* Creation & Action Bar */}
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '14px' }}>
              <span style={{ fontSize: '12.5px', color: 'var(--text-dim)' }}>
                当前共有 <strong>{workflows.value.length}</strong> 个工作流资产
              </span>
              <div style={{ display: 'flex', gap: '8px' }}>
                <button
                  type="button"
                  style={miniButtonStyle(true)}
                  onClick={() => { showNew.value = !showNew.value }}
                >
                  {showNew.value ? '取消创建' : '+ 新建工作流'}
                </button>
                <button
                  type="button"
                  style={miniButtonStyle(false)}
                  onClick={loadDemoTemplate}
                >
                  加载演示模板
                </button>
              </div>
            </div>

            {/* Create Form */}
            {showNew.value && (
              <div style={{
                background: 'var(--surface)', padding: '14px', borderRadius: '10px',
                border: '1px solid var(--border)', marginBottom: '14px', boxShadow: 'var(--shadow-sm)',
              }}>
                <div style={{ display: 'flex', gap: '10px', flexDirection: 'column' as const }}>
                  <div>
                    <label style={{ fontSize: '11px', fontWeight: 600, color: 'var(--text-dim)', display: 'block', marginBottom: '4px' }}>工作流名称</label>
                    <input
                      style={{
                        width: '100%', padding: '6px 10px', background: 'var(--bg)',
                        border: '1px solid var(--border)', borderRadius: '6px',
                        color: 'var(--text)', fontSize: '12.5px',
                      }}
                      value={newName.value}
                      onInput={(e: Event) => { newName.value = (e.target as HTMLInputElement).value }}
                      placeholder="输入名称，例如：智能纪要与翻译..."
                    />
                  </div>
                  <div>
                    <label style={{ fontSize: '11px', fontWeight: 600, color: 'var(--text-dim)', display: 'block', marginBottom: '4px' }}>描述（可选）</label>
                    <input
                      style={{
                        width: '100%', padding: '6px 10px', background: 'var(--bg)',
                        border: '1px solid var(--border)', borderRadius: '6px',
                        color: 'var(--text)', fontSize: '12.5px',
                      }}
                      value={newDesc.value}
                      onInput={(e: Event) => { newDesc.value = (e.target as HTMLInputElement).value }}
                      placeholder="简短说明..."
                    />
                  </div>
                  <div style={{ display: 'flex', gap: '8px', justifyContent: 'flex-end', marginTop: '4px' }}>
                    <button type="button" style={miniButtonStyle(true)} onClick={createWf}>创建并编辑</button>
                    <button type="button" style={miniButtonStyle(false)} onClick={() => { showNew.value = false }}>取消</button>
                  </div>
                </div>
              </div>
            )}

            {/* List Content */}
            {loading.value ? EmptyState('加载中…')
              : !workflows.value.length ? EmptyState('暂无工作流。点击“新建工作流”或“加载演示模板”开始。')
              : workflows.value.map((wf: any) => {
                  const enabled = isWorkflowEnabled(wf.id)
                  return rowDiv(
                    <>
                      <span style={{
                        width: '8px', height: '8px', borderRadius: '50%',
                        background: enabled ? 'var(--success)' : 'var(--text-dim)',
                        flexShrink: 0, display: 'block',
                      }}></span>
                      <div style={{ flex: 1, overflow: 'hidden' }}>
                        <div style={{ fontWeight: 600, color: 'var(--text-strong)', display: 'flex', alignItems: 'center', gap: '6px' }}>
                          {wf.name || wf.id}
                          <code style={{ fontSize: '10.5px', padding: '1px 6px', borderRadius: '4px', background: 'var(--bg)', border: '1px solid var(--border-subtle)', color: 'var(--text-dim)' }}>
                            {wf.id}
                          </code>
                        </div>
                        <div style={{ fontSize: '11.5px', color: 'var(--text-dim)', marginTop: '2px', textOverflow: 'ellipsis', overflow: 'hidden', whiteSpace: 'nowrap' }}>
                          {wf.description || '点击右侧“画布编排”配置 Start → LLM / Tool → End 流程'}
                        </div>
                      </div>

                      <div style={{ display: 'flex', alignItems: 'center', gap: '6px', flexShrink: 0 }}>
                        <button
                          type="button"
                          onClick={() => toggleWorkflowStatus(wf.id)}
                          style={{
                            padding: '3px 8px',
                            border: '1px solid var(--border)',
                            borderRadius: '6px',
                            background: enabled ? 'var(--accent-bg)' : 'var(--surface)',
                            color: enabled ? 'var(--accent-text)' : 'var(--text-dim)',
                            fontSize: '11px',
                            cursor: 'pointer',
                            fontWeight: 600,
                          }}
                        >
                          {enabled ? '● 已启用' : '○ 已禁用'}
                        </button>
                        <button
                          type="button"
                          style={miniButtonStyle(true)}
                          onClick={() => openInInlineCanvas(wf.id)}
                        >
                          画布编排
                        </button>
                        <button
                          type="button"
                          style={miniButtonStyle(false)}
                          onClick={() => openWfEditor(wf.id)}
                        >
                          ↗ 窗口
                        </button>
                      </div>
                    </>,
                  )
                })
            }
          </div>
          </>
        )}
      </div>
    )
  },
})
