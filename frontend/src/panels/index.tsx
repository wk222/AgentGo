import { defineComponent, ref, onMounted, type PropType, type VNode } from 'vue'
import { wailsCall } from '../wails'
import WorkflowEditorComp from './WorkflowEditor'
import InnerAppHost from '../InnerAppHost'

/* ── shared helpers ─────────────────────────────────────────────────── */
const panelBodyStyle = {
  display: 'flex', flexDirection: 'column' as const,
  height: '100%', overflow: 'hidden',
}
const scrollStyle = { flex: 1, overflowY: 'auto' as const, padding: '16px 20px' }

function EmptyState(msg: string) {
  return (
    <div style={{ textAlign: 'center', color: 'var(--text-dim)', padding: '40px 0', fontSize: '13px' }}>
      {msg}
    </div>
  )
}

function PanelHeader(icon: string, title: string) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: '10px',
      padding: '18px 20px 14px', borderBottom: '1px solid var(--border-subtle)', flexShrink: 0,
    }}>
      <span style={{ fontSize: '18px' }}>{icon}</span>
      <span style={{ fontSize: '15px', fontWeight: 700, color: 'var(--text-strong)' }}>{title}</span>
    </div>
  )
}

function badge(label: string, color?: string) {
  return (
    <span style={{
      fontSize: '11px', padding: '1px 7px', borderRadius: '999px', flexShrink: 0,
      background: color || 'var(--accent-bg)', color: 'var(--accent-text)', fontWeight: 600,
    }}>{label}</span>
  )
}

const miniButtonStyle = (primary = false) => ({
  padding: '3px 8px',
  border: primary ? 'none' : '1px solid var(--border)',
  borderRadius: 'var(--radius-md)',
  background: primary ? 'var(--accent-bg)' : 'transparent',
  color: primary ? 'var(--accent-text)' : 'var(--text-dim)',
  fontSize: '11px',
  cursor: 'pointer',
  fontWeight: primary ? 700 : 500,
  whiteSpace: 'nowrap' as const,
})

function rowDiv(children: VNode | VNode[]) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: '10px',
      padding: '9px 12px', borderRadius: 'var(--radius-md)',
      background: 'var(--surface-2)', marginBottom: '6px', fontSize: '13px', color: 'var(--text)',
    }}>{children}</div>
  )
}

const statusColor = (s: string) => {
  if (!s) return 'var(--text-dim)'
  if (s === 'active' || s === 'connected' || s === 'enabled' || s === 'done' || s === 'completed') return '#5cb85c'
  if (s === 'running') return '#3b9eff'
  if (s === 'failed' || s === 'error') return '#e05c5c'
  return 'var(--text-dim)'
}

/* ── Workflow Panel (VueFlow editor) ─────────────────────────────────── */
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
        alert('修改状态失败: ' + (e.message || e))
      }
    }

    const openWfEditor = async (wfId: string) => {
      try {
        await wailsCall('OpenWorkflowWindow', wfId)
      } catch (e: any) {
        alert('无法打开独立窗口: ' + (e.message || e))
      }
    }

    const createWf = async () => {
      const name = newName.value.trim()
      if (!name) return
      try {
        const id = `wf-${Date.now()}`
        await wailsCall('SaveWorkflow', { id, name, description: newDesc.value.trim(), nodes: [], edges: [] })
        
        // Save empty flowgram document
        const emptyDoc = {
          nodes: [
            { id: 'start', type: 'start', title: '开始', x: 100, y: 150 },
            { id: 'end', type: 'end', title: '结束', x: 500, y: 150 }
          ],
          edges: []
        }
        await wailsCall('SaveWorkflowFlowgram', id, JSON.stringify(emptyDoc))
        
        showNew.value = false
        newName.value = ''
        newDesc.value = ''
        
        // Reload list
        await loadWorkflows()
        
        // Auto open new window for editing
        await openWfEditor(id)
      } catch (e: any) {
        alert('创建失败: ' + (e.message || e))
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

    onMounted(loadWorkflows)

    return () => (
      <div style={panelBodyStyle}>
        {PanelHeader('⚡', 'Workflow 工作流')}
        <div style={scrollStyle}>
          {statusMsg.value && (
            <div style={{ color: 'var(--accent)', fontSize: '12px', marginBottom: '10px' }}>{statusMsg.value}</div>
          )}

          {/* Creation & Action Bar */}
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
            <span style={{ fontSize: '13px', color: 'var(--text-dim)' }}>
              当前共有 <strong>{workflows.value.length}</strong> 个工作流
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
                演示模板
              </button>
            </div>
          </div>

          {/* Create Form */}
          {showNew.value && (
            <div style={{
              background: 'var(--surface-2)', padding: '14px', borderRadius: 'var(--radius-md)',
              border: '1px solid var(--border-subtle)', marginBottom: '16px'
            }}>
              <div style={{ display: 'flex', gap: '10px', flexDirection: 'column' as const }}>
                <div>
                  <label style={{ fontSize: '11px', fontWeight: 600, color: 'var(--text-dim)', display: 'block', marginBottom: '4px' }}>工作流名称</label>
                  <input
                    style={{
                      width: '100%', padding: '6px 10px', background: 'var(--surface)',
                      border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)',
                      color: 'var(--text)', fontSize: '13px'
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
                      width: '100%', padding: '6px 10px', background: 'var(--surface)',
                      border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)',
                      color: 'var(--text)', fontSize: '13px'
                    }}
                    value={newDesc.value}
                    onInput={(e: Event) => { newDesc.value = (e.target as HTMLInputElement).value }}
                    placeholder="简短说明..."
                  />
                </div>
                <div style={{ display: 'flex', gap: '8px', justifyContent: 'flex-end', marginTop: '6px' }}>
                  <button type="button" style={miniButtonStyle(true)} onClick={createWf}>创建并打开编辑器</button>
                  <button type="button" style={miniButtonStyle(false)} onClick={() => { showNew.value = false }}>取消</button>
                </div>
              </div>
            </div>
          )}

          {/* List Content */}
          {loading.value ? EmptyState('加载中…')
            : !workflows.value.length ? EmptyState('暂无工作流。点击“新建工作流”或“演示模板”开始。')
            : workflows.value.map((wf: any) => {
                const enabled = isWorkflowEnabled(wf.id)
                return rowDiv(
                  <>
                    <span style={{ 
                      width: '8px', height: '8px', borderRadius: '50%', 
                      background: enabled ? 'var(--accent)' : 'var(--text-dim)', 
                      flexShrink: 0, display: 'block' 
                    }}></span>
                    <div style={{ flex: 1, overflow: 'hidden' }}>
                      <div style={{ fontWeight: 600, color: 'var(--text-strong)', display: 'flex', alignItems: 'center', gap: '6px' }}>
                        {wf.name || wf.id}
                        <span style={{ fontSize: '10px', padding: '1px 5px', borderRadius: '4px', background: 'var(--surface-3)', color: 'var(--text-dim)' }}>
                          {wf.id}
                        </span>
                      </div>
                      <div style={{ fontSize: '11px', color: 'var(--text-dim)', marginTop: '2px', textOverflow: 'ellipsis', overflow: 'hidden', whiteSpace: 'nowrap' }}>
                        {wf.description || '暂无描述信息'}
                      </div>
                    </div>
                    
                    {/* Status & Action */}
                    <div style={{ display: 'flex', alignItems: 'center', gap: '8px', flexShrink: 0 }}>
                      <button
                        type="button"
                        onClick={() => toggleWorkflowStatus(wf.id)}
                        style={{
                          padding: '3px 8px',
                          border: '1px solid var(--border)',
                          borderRadius: 'var(--radius-md)',
                          background: enabled ? 'transparent' : 'var(--surface)',
                          color: enabled ? 'var(--accent)' : 'var(--text-faint)',
                          fontSize: '11px',
                          cursor: 'pointer',
                          fontWeight: 500,
                        }}
                      >
                        {enabled ? '已启用' : '已禁用'}
                      </button>
                      <button
                        type="button"
                        style={miniButtonStyle(true)}
                        onClick={() => openWfEditor(wf.id)}
                      >
                        编辑
                      </button>
                    </div>
                  </>
                )
              })
          }
        </div>
      </div>
    )
  },
})

/* ── Tasks Panel ─────────────────────────────────────────────────────── */
export const TasksPanel = defineComponent({
  name: 'TasksPanel',
  setup() {
    const tasks = ref<any[]>([])
    const jobs = ref<any[]>([])
    const loading = ref(true)
    onMounted(async () => {
      try { tasks.value = (await wailsCall('ListBackgroundTasks', 30)) || [] } catch {}
      try { jobs.value = (await wailsCall('ListScheduledJobs')) || [] } catch {}
      loading.value = false
    })
    return () => (
      <div style={panelBodyStyle}>
        {PanelHeader('📋', '任务管理')}
        <div style={scrollStyle}>
          {loading.value ? EmptyState('加载中…')
            : (!tasks.value.length && !jobs.value.length) ? EmptyState('暂无任务。通过对话让 Agent 创建任务。')
            : <>
              {tasks.value.length > 0 && <>
                <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--text-dim)', textTransform: 'uppercase' as const, letterSpacing: '.06em', marginBottom: '8px' }}>后台任务</div>
                {tasks.value.map((t: any) => rowDiv(<>
                  <span style={{ width: '8px', height: '8px', borderRadius: '50%', background: statusColor(t.status), flexShrink: 0, display: 'block' }}></span>
                  <div style={{ flex: 1, overflow: 'hidden' }}>
                    <div style={{ fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{t.goal || t.title || t.id}</div>
                    {t.steps ? <div style={{ fontSize: '11px', color: 'var(--text-dim)' }}>{t.steps} 步</div> : null}
                  </div>
                  {badge(t.status || '未知', statusColor(t.status))}
                </>))}
              </>}
              {jobs.value.length > 0 && <>
                <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--text-dim)', textTransform: 'uppercase' as const, letterSpacing: '.06em', margin: '12px 0 8px' }}>定时任务</div>
                {jobs.value.map((j: any) => rowDiv(<>
                  <span style={{ fontSize: '14px', flexShrink: 0 }}>🕐</span>
                  <div style={{ flex: 1, overflow: 'hidden' }}>
                    <div style={{ fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{j.title || j.id}</div>
                    <div style={{ fontSize: '11px', color: 'var(--text-dim)' }}>{j.spec}</div>
                  </div>
                </>))}
              </>}
            </>
          }
        </div>
      </div>
    )
  },
})

/* ── Capability Center Panel ─────────────────────────────────────────── */
export const CapabilityPanel = defineComponent({
  name: 'CapabilityPanel',
  props: {
    onSendToAgent: { type: Function as PropType<(text: string) => void>, default: undefined },
  },
  setup(props) {
    const grants = ref<any[]>([])
    const tools = ref<string[]>([])
    const workflows = ref<any[]>([])
    const apps = ref<any[]>([])
    const skills = ref<any[]>([])
    const loading = ref(true)
    const status = ref('')
    const kindFilter = ref('all')
    const statusFilter = ref('active')

    const load = async () => {
      loading.value = true
      status.value = ''
      try { grants.value = (await wailsCall('ListCapabilityGrants', '')) || [] } catch (e: any) { status.value = e?.message || '能力列表加载失败' }
      try { tools.value = (await wailsCall('ListRegisteredTools')) || [] } catch {}
      try { workflows.value = (await wailsCall('ListWorkflows')) || [] } catch {}
      try { apps.value = (await wailsCall('ListInnerApps', 50)) || [] } catch {}
      try { skills.value = (await wailsCall('ListSkills')) || [] } catch {}
      loading.value = false
    }

    onMounted(() => { void load() })

    const counts = () => ({
      grants: grants.value.length,
      tools: tools.value.length,
      workflows: workflows.value.length,
      apps: apps.value.length,
      skills: skills.value.length,
    })

    const activeStatuses = new Set(['draft', 'verified', 'published'])
    const visibleGrants = () => grants.value.filter((g: any) => {
      if (kindFilter.value !== 'all' && g.kind !== kindFilter.value) return false
      if (statusFilter.value === 'active') return activeStatuses.has(String(g.status || 'published'))
      if (statusFilter.value === 'retired') return ['deprecated', 'retired'].includes(String(g.status || ''))
      return true
    })

    const transition = async (g: any, next: string) => {
      status.value = ''
      try {
        await wailsCall('TransitionCapabilityGrant', g.id, next)
        await load()
      } catch (e: any) {
        status.value = e?.message || '状态切换失败'
      }
    }

    const verify = async (g: any, result = 'pass') => {
      status.value = ''
      try {
        await wailsCall('VerifyCapabilityGrant', g.id, result)
        await load()
      } catch (e: any) {
        status.value = e?.message || '验证失败'
      }
    }

    const askAgent = (g: any) => {
      props.onSendToAgent?.(`请评估这个 AgentGo 能力是否应该保留、启用或替换。\n\n能力：${g.kind}/${g.name}\nID：${g.id}\nScope：${g.scope || ''}\nStatus：${g.status || ''}\nRisk：${g.risk_level || ''}\nMetadata：${JSON.stringify(g.metadata || {}, null, 2)}`)
    }

    const statusChipColor = (s: string) => {
      if (s === 'published' || s === 'verified') return '#5cb85c'
      if (s === 'draft') return '#3b9eff'
      if (s === 'deprecated' || s === 'retired') return 'var(--text-dim)'
      return 'var(--text-dim)'
    }

    const kindOptions = ['all', 'tool', 'workflow', 'app', 'skill', 'channel', 'agent', 'memory']

    return () => {
      const c = counts()
      const rows = visibleGrants()
      return (
        <div style={panelBodyStyle}>
          {PanelHeader('🛡', '能力中心')}
          <div style={scrollStyle}>
            <div style={{ border: '1px solid var(--border-subtle)', borderRadius: 'var(--radius-md)', background: 'var(--surface-2)', color: 'var(--text-dim)', fontSize: '12px', lineHeight: 1.5, padding: '9px 11px', marginBottom: '12px' }}>
              这里直接面向后端能力注册表，统一管理工具、Workflow、Inner App、Skill 和 Channel 的可用性、风险与验证状态。
            </div>

            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(5, minmax(0, 1fr))', gap: '8px', marginBottom: '12px' }}>
              {[
                ['Capabilities', c.grants],
                ['Tools', c.tools],
                ['Workflows', c.workflows],
                ['Inner Apps', c.apps],
                ['Skills', c.skills],
              ].map(([label, value]) => (
                <div key={String(label)} style={{ border: '1px solid var(--border-subtle)', borderRadius: 'var(--radius-md)', background: 'var(--surface-2)', padding: '10px' }}>
                  <div style={{ fontSize: '18px', fontWeight: 800, color: 'var(--text-strong)' }}>{value}</div>
                  <div style={{ fontSize: '11px', color: 'var(--text-dim)', marginTop: '2px' }}>{label}</div>
                </div>
              ))}
            </div>

            <div style={{ display: 'flex', gap: '6px', alignItems: 'center', marginBottom: '12px', flexWrap: 'wrap' }}>
              {kindOptions.map(k => (
                <button key={k} onClick={() => { kindFilter.value = k }} style={{
                  padding: '3px 10px', borderRadius: '999px', fontSize: '12px', cursor: 'pointer',
                  border: '1px solid var(--border)',
                  background: kindFilter.value === k ? 'var(--accent-bg)' : 'transparent',
                  color: kindFilter.value === k ? 'var(--accent-text)' : 'var(--text-dim)',
                }}>{k === 'all' ? '全部' : k}</button>
              ))}
              {(['active', 'all', 'retired'] as const).map(s => (
                <button key={s} onClick={() => { statusFilter.value = s }} style={miniButtonStyle(statusFilter.value === s)}>
                  {s === 'active' ? '活跃' : s === 'retired' ? '已退役' : '全部状态'}
                </button>
              ))}
              <button style={miniButtonStyle()} onClick={load}>刷新</button>
            </div>

            {status.value && <div style={{ color: '#e05c5c', fontSize: '12px', marginBottom: '10px' }}>{status.value}</div>}
            {loading.value ? EmptyState('加载中…')
              : rows.length === 0 ? EmptyState('暂无匹配能力。运行一次对话、同步 Inner App 或创建 Workflow 后会出现在这里。')
              : rows.map((g: any) => rowDiv(<>
                <span style={{ width: '8px', height: '8px', borderRadius: '50%', background: statusChipColor(g.status || 'published'), flexShrink: 0, display: 'block' }}></span>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '6px', minWidth: 0 }}>
                    <span style={{ fontWeight: 650, color: 'var(--text-strong)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{g.name || g.id}</span>
                    {badge(g.kind || 'capability')}
                    {g.risk_level ? badge(g.risk_level, g.risk_level === 'high' ? '#e05c5c' : undefined) : null}
                  </div>
                  <div style={{ fontSize: '11px', color: 'var(--text-dim)', marginTop: '2px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {g.id} · {g.scope || 'global'}{g.verify_result ? ` · verify:${g.verify_result}` : ''}
                  </div>
                </div>
                {badge(g.status || 'published', statusChipColor(g.status || 'published'))}
                <button style={miniButtonStyle()} onClick={() => verify(g)}>验证</button>
                {g.status === 'published'
                  ? <button style={miniButtonStyle()} onClick={() => transition(g, 'retired')}>停用</button>
                  : <button style={miniButtonStyle(true)} onClick={() => transition(g, 'published')}>发布</button>}
                <button style={miniButtonStyle()} disabled={!props.onSendToAgent} onClick={() => askAgent(g)}>让 Agent 评估</button>
              </>))}
          </div>
        </div>
      )
    }
  },
})

/* ── Skills Panel ────────────────────────────────────────────────────── */
export const SkillsPanel = defineComponent({
  name: 'SkillsPanel',
  props: {
    onSendToAgent: { type: Function as PropType<(text: string) => void>, default: undefined },
  },
  setup(props) {
    const skills = ref<any[]>([])
    const tools = ref<string[]>([])
    const loading = ref(true)
    const tab = ref<'tools' | 'skills'>('tools')
    const status = ref('')
    const load = async () => {
      loading.value = true
      try { skills.value = (await wailsCall('ListSkills')) || [] } catch {}
      try { tools.value = (await wailsCall('ListRegisteredTools')) || [] } catch {}
      loading.value = false
    }
    onMounted(async () => {
      await load()
    })
    const sendSkill = async (sk: any) => {
      try {
        const ctx = await wailsCall('GetSkillContext', [sk.id])
        props.onSendToAgent?.(`请使用这个 Skill 的上下文来处理当前 AgentGo 项目问题。\n\nSkill：${sk.name || sk.id}\n描述：${sk.description || ''}\n\n${ctx || ''}`)
      } catch (e: any) {
        status.value = e?.message || '读取 Skill 上下文失败'
      }
    }
    const sendTool = (name: string) => {
      props.onSendToAgent?.(`请判断是否需要使用工具 \`${name}\` 解决当前问题；如果需要，请说明输入、风险和预期输出后再执行。`)
    }
    return () => (
      <div style={panelBodyStyle}>
        {PanelHeader('⭐', 'Skills & Tools')}
        <div style={scrollStyle}>
          {loading.value ? EmptyState('加载中…') : <>
            <div style={{ display: 'flex', gap: '6px', marginBottom: '14px', flexWrap: 'wrap' }}>
              {(['tools', 'skills'] as const).map(t => (
                <button key={t} onClick={() => tab.value = t} style={{
                  padding: '3px 12px', borderRadius: '999px', fontSize: '12px', cursor: 'pointer',
                  border: '1px solid var(--border)',
                  background: tab.value === t ? 'var(--accent-bg)' : 'transparent',
                  color: tab.value === t ? 'var(--accent-text)' : 'var(--text-dim)',
                }}>{t === 'tools' ? `工具 (${tools.value.length})` : `Skills (${skills.value.length})`}</button>
              ))}
              <button style={miniButtonStyle()} onClick={load}>刷新</button>
            </div>
            {status.value && <div style={{ color: '#e05c5c', fontSize: '12px', marginBottom: '10px' }}>{status.value}</div>}
            {tab.value === 'tools' && (
              tools.value.length === 0 ? EmptyState('暂无已注册工具')
              : tools.value.map((name: string, i: number) => rowDiv(<>
                <span style={{ fontSize: '15px', flexShrink: 0 }}>🔧</span>
                <span style={{ flex: 1, fontFamily: 'monospace', fontSize: '12.5px' }}>{name}</span>
                <button style={miniButtonStyle()} disabled={!props.onSendToAgent} onClick={() => sendTool(name)}>让 Agent 评估</button>
              </>))
            )}
            {tab.value === 'skills' && (
              skills.value.length === 0 ? EmptyState('暂无 Skill。可在 skills/ 目录添加 Markdown 文件。')
              : skills.value.map((sk: any, i: number) => rowDiv(<>
                <div style={{ flex: 1, overflow: 'hidden' }}>
                  <div style={{ fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{sk.name}</div>
                  {sk.description ? <div style={{ fontSize: '11px', color: 'var(--text-dim)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{sk.description}</div> : null}
                </div>
                {sk.scope ? badge(sk.scope) : null}
                <button style={miniButtonStyle(true)} disabled={!props.onSendToAgent} onClick={() => sendSkill(sk)}>使用</button>
              </>))
            )}
          </>}
        </div>
      </div>
    )
  },
})

/* ── Channels Panel ──────────────────────────────────────────────────── */
export const ChannelsPanel = defineComponent({
  name: 'ChannelsPanel',
  setup() {
    const items = ref<any[]>([])
    const loading = ref(true)
    onMounted(async () => {
      try { items.value = (await wailsCall('ListChannels')) || [] } catch {}
      loading.value = false
    })
    return () => (
      <div style={panelBodyStyle}>
        {PanelHeader('🌐', 'Channels')}
        <div style={scrollStyle}>
          {loading.value ? EmptyState('加载中…')
            : !items.value.length ? EmptyState('暂无 Channel。可通过环境变量启用 Slack/Telegram 等集成。')
            : items.value.map((ch: any) => rowDiv(<>
              <span style={{ width: '8px', height: '8px', borderRadius: '50%', background: statusColor(ch.state || ch.status), flexShrink: 0, display: 'block' }}></span>
              <div style={{ flex: 1, overflow: 'hidden' }}>
                <div style={{ fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{ch.channel || ch.name || ch.id}</div>
                {ch.kind ? <div style={{ fontSize: '11px', color: 'var(--text-dim)' }}>{ch.kind}</div> : null}
              </div>
              {badge(ch.state || ch.status || '未知', statusColor(ch.state || ch.status))}
            </>))}
        </div>
      </div>
    )
  },
})

/* ── Apps Panel ──────────────────────────────────────────────────────── */
export const AppsPanel = defineComponent({
  name: 'AppsPanel',
  props: {
    onSendToAgent: { type: Function as PropType<(text: string) => void>, default: undefined },
  },
  setup(props) {
    const items = ref<any[]>([])
    const loading = ref(true)
    const status = ref('')
    const showBuilder = ref(false)
    const building = ref(false)
    const viewerApp = ref<any | null>(null)
    const buildForm = ref({
      name: '',
      displayName: '',
      description: '',
      mode: 'static',
      workflowID: '',
      systemPrompt: '',
      overwrite: false,
    })
    const load = async () => {
      loading.value = true
      try { items.value = (await wailsCall('ListInnerApps', 50)) || [] } catch {}
      loading.value = false
    }
    const appName = (app: any) => String(app.name || app.id || '').replace(/^app:/, '')
    const hasUI = (app: any) => !!(app.has_ui || app.kind === 'ui' || app.bundle_path || app.url)
    onMounted(() => { void load() })
    const askAgent = (app: any) => {
      props.onSendToAgent?.(`请评估并在合适时调用内置应用 ${appName(app)}。\n\n应用描述：${app.description || ''}\n请说明它适合处理什么输入、需要哪些权限或上下文。`)
    }
    const openUI = async (app: any) => {
      status.value = ''
      const name = appName(app)
      try {
        const r = await wailsCall('OpenInnerAppWindow', name)
        if (r?.success) {
          viewerApp.value = null
          status.value = `已在独立窗口打开 ${name}`
          return
        }
        status.value = r?.error ? `独立窗口不可用，已切到内嵌预览：${r.error}` : ''
      } catch {}
      viewerApp.value = app
    }
    const pingApp = async (app: any) => {
      status.value = ''
      try {
        const r = await wailsCall('InvokeInnerApp', appName(app), 'health check from UI', '')
        status.value = r?.success ? (r.content || '调用成功') : (r?.error || '调用失败')
      } catch (e: any) { status.value = e?.message || '调用失败' }
    }
    const buildApp = async () => {
      const form = buildForm.value
      if (!form.name.trim()) {
        status.value = '请先填写应用名'
        return
      }
      building.value = true
      status.value = ''
      try {
        const r = await wailsCall(
          'BuildInnerAppIteratively',
          form.name.trim(),
          form.displayName.trim(),
          form.description.trim(),
          form.mode,
          form.workflowID.trim(),
          form.systemPrompt.trim(),
          3,
          form.overwrite,
        )
        status.value = r?.success
          ? `已生成 ${r.app_name || form.name}：${r.message || '完成'}`
          : (r?.error || '生成失败')
        await load()
      } catch (e: any) {
        status.value = e?.message || '生成失败'
      } finally {
        building.value = false
      }
    }
    return () => (
      <div style={panelBodyStyle}>
        {PanelHeader('📦', '内置应用')}
        <div style={scrollStyle}>
          <div style={{ display: 'flex', gap: '6px', marginBottom: '12px', flexWrap: 'wrap' }}>
            <button style={miniButtonStyle(true)} onClick={() => showBuilder.value = !showBuilder.value}>
              {showBuilder.value ? '收起生成器' : '生成 Inner App'}
            </button>
            <button style={miniButtonStyle()} onClick={load}>刷新</button>
          </div>
          {showBuilder.value && (
            <div style={{ border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', padding: '12px', marginBottom: '12px', background: 'var(--surface-2)' }}>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '8px', marginBottom: '8px' }}>
                <input style={{ padding: '7px 9px', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', background: 'var(--input-bg)', color: 'var(--text)' }} placeholder="应用名 demo_app" value={buildForm.value.name} onInput={(e: Event) => buildForm.value = { ...buildForm.value, name: (e.target as HTMLInputElement).value }} />
                <input style={{ padding: '7px 9px', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', background: 'var(--input-bg)', color: 'var(--text)' }} placeholder="显示名" value={buildForm.value.displayName} onInput={(e: Event) => buildForm.value = { ...buildForm.value, displayName: (e.target as HTMLInputElement).value }} />
              </div>
              <textarea style={{ width: '100%', minHeight: '70px', padding: '8px 9px', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', background: 'var(--input-bg)', color: 'var(--text)', resize: 'vertical', marginBottom: '8px' }} placeholder="描述这个 app 要解决什么问题，AI 会基于它生成 UI bundle。" value={buildForm.value.description} onInput={(e: Event) => buildForm.value = { ...buildForm.value, description: (e.target as HTMLTextAreaElement).value }} />
              <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap', alignItems: 'center' }}>
                <select style={{ padding: '6px 9px', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', background: 'var(--input-bg)', color: 'var(--text)' }} value={buildForm.value.mode} onChange={(e: Event) => buildForm.value = { ...buildForm.value, mode: (e.target as HTMLSelectElement).value }}>
                  <option value="static">静态 UI</option>
                  <option value="chat">聊天 UI</option>
                  <option value="workflow">工作流 UI</option>
                </select>
                {buildForm.value.mode === 'workflow' && (
                  <input style={{ padding: '6px 9px', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', background: 'var(--input-bg)', color: 'var(--text)' }} placeholder="workflow_id" value={buildForm.value.workflowID} onInput={(e: Event) => buildForm.value = { ...buildForm.value, workflowID: (e.target as HTMLInputElement).value }} />
                )}
                <label style={{ display: 'flex', alignItems: 'center', gap: '5px', color: 'var(--text-dim)', fontSize: '12px' }}>
                  <input type="checkbox" checked={buildForm.value.overwrite} onChange={(e: Event) => buildForm.value = { ...buildForm.value, overwrite: (e.target as HTMLInputElement).checked }} />
                  覆盖同名
                </label>
                <button style={miniButtonStyle(true)} disabled={building.value} onClick={buildApp}>
                  {building.value ? '生成中…' : '开始生成'}
                </button>
              </div>
            </div>
          )}
          {status.value && <div style={{ color: 'var(--text-dim)', fontSize: '12px', marginBottom: '12px', lineHeight: 1.5 }}>{status.value}</div>}
          {viewerApp.value && (
            <div class="inner-app-viewer">
              <div class="inner-app-viewer-head">
                <span>{appName(viewerApp.value)}</span>
                <button style={miniButtonStyle()} onClick={() => { viewerApp.value = null }}>关闭</button>
              </div>
              <InnerAppHost appName={appName(viewerApp.value)} />
            </div>
          )}
          {loading.value ? EmptyState('加载中…')
            : !items.value.length ? EmptyState('暂无内置应用。通过对话激活应用插件。')
            : items.value.map((app: any) => rowDiv(<>
              <span style={{ fontSize: '20px', flexShrink: 0 }}>{app.icon || '📦'}</span>
              <div style={{ flex: 1, overflow: 'hidden' }}>
                <div style={{ fontWeight: 500 }}>{app.name || app.id}</div>
                {app.description ? <div style={{ fontSize: '11px', color: 'var(--text-dim)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{app.description}</div> : null}
              </div>
              {badge(hasUI(app) ? 'UI' : 'Callable App', hasUI(app) ? '#3b9eff' : undefined)}
              {app.enabled !== undefined ? badge(app.enabled ? '启用' : '未启用', app.enabled ? '#5cb85c' : undefined) : null}
              {hasUI(app) ? <button style={miniButtonStyle(true)} onClick={() => openUI(app)}>打开 UI</button> : null}
              <button style={miniButtonStyle(true)} disabled={!props.onSendToAgent} onClick={() => askAgent(app)}>调用建议</button>
              <button style={miniButtonStyle()} onClick={() => pingApp(app)}>健康检查</button>
            </>))}
        </div>
      </div>
    )
  },
})
