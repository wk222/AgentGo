import { defineComponent, ref, onMounted, type PropType } from 'vue'
import { wailsCall } from '../../shared/wails'
import { panelBodyStyle, scrollStyle, EmptyState, PanelHeader, badge, miniButtonStyle, rowDiv } from '../../shared/ui/panelKit'

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
