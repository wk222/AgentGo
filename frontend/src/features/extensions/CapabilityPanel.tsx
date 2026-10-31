import { defineComponent, ref, onMounted, type PropType } from 'vue'
import { wailsCall } from '../../shared/wails'
import { panelBodyStyle, scrollStyle, EmptyState, PanelHeader, badge, miniButtonStyle, rowDiv } from '../../shared/ui/panelKit'

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
                    {g.risk_level ? badge(g.risk_level, g.risk_level === 'high' || g.risk_level === 'critical' ? '#e05c5c' : undefined) : null}
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
