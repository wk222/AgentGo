import { defineComponent, ref, computed, onMounted, watch, type PropType } from 'vue'
import { wailsCall } from '../../shared/wails'
import { useChat } from '../../stores/useChat'
import { notify } from '../../shared/ui/notify'

export const ContextXRayView = defineComponent({
  name: 'ContextXRayView',
  props: {
    onSendToAgent: { type: Function as PropType<(text: string) => void>, default: undefined },
  },
  setup(props) {
    const chat = useChat()
    const promptPreview = ref('')
    const memories = ref<any[]>([])
    const skills = ref<any[]>([])
    const loading = ref(false)
    const distilling = ref(false)

    const stats = computed(() => {
      const msgs = chat.messages.value
      const userTurns = msgs.filter(m => m.role === 'user' && !m.type).length
      const toolCalls = msgs.filter(m => m.type === 'tool_call').length
      return { userTurns, toolCalls, total: msgs.length }
    })

    const loadXRay = async () => {
      loading.value = true
      const scope = chat.sessionId.value || 'global'
      try {
        const p = await wailsCall('MemoryContextPrompt', scope)
        promptPreview.value = String(p?.prompt || '')
      } catch {}
      try {
        memories.value = ((await wailsCall('ListMemories', 10)) || []).slice(0, 8)
      } catch {}
      try {
        skills.value = ((await wailsCall('ListSkills')) || []).slice(0, 10)
      } catch {}
      loading.value = false
    }

    const distillNow = async () => {
      distilling.value = true
      const scope = chat.sessionId.value || 'global'
      try {
        const r = await wailsCall('MemoryDistill', scope)
        if (r?.success !== false) {
          notify.success('已提炼并更新当前会话记忆')
          await loadXRay()
        } else {
          notify.error(r?.error || '记忆提炼失败')
        }
      } catch (e: any) {
        notify.error(e?.message || '记忆提炼异常')
      } finally {
        distilling.value = false
      }
    }

    const feedback = async (id: string, signal: 'positive' | 'negative') => {
      try {
        const r = await wailsCall('MemoryFeedback', id, signal)
        if (r?.success !== false) {
          notify.info(signal === 'positive' ? '已强化该条记忆权重' : '已降低该条记忆权重')
          await loadXRay()
        }
      } catch {}
    }

    watch(() => chat.sessionId.value, () => { void loadXRay() })
    onMounted(() => { void loadXRay() })

    const cardBox = {
      border: '1px solid var(--border)',
      borderRadius: 'var(--radius-md)',
      background: 'var(--surface)',
      padding: '10px 12px',
      marginBottom: '10px',
    }

    const btnSmall = (accent = false) => ({
      border: accent ? '1px solid var(--accent)' : '1px solid var(--border)',
      borderRadius: '4px',
      background: accent ? 'var(--accent-bg)' : 'var(--bg)',
      color: accent ? 'var(--accent-text)' : 'var(--text-dim)',
      fontSize: '11px',
      padding: '2px 7px',
      cursor: 'pointer',
    })

    return () => (
      <div class="rp-content" style={{ padding: '12px', overflowY: 'auto', fontSize: '12px' }}>
        {/* 1. SessionSpine & Prefix Cache 状态 */}
        <div style={cardBox}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '6px' }}>
            <strong style={{ color: 'var(--accent-text)', fontSize: '12px' }}>⚡ SessionSpine · Cache-First</strong>
            <button type="button" style={btnSmall()} onClick={loadXRay} disabled={loading.value}>
              {loading.value ? '刷新中…' : '刷新'}
            </button>
          </div>
          <div style={{ color: 'var(--text-dim)', fontSize: '11px', lineHeight: 1.5, marginBottom: '8px' }}>
            前缀幂等锁定 · 6 轮活跃窗口 · 超长工具输出头 5/8 + 尾 3/8 确定性裁剪（报错豁免）。
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '6px', textAlign: 'center' }}>
            <div style={{ background: 'var(--bg)', padding: '6px', borderRadius: '4px', border: '1px solid var(--border-subtle)' }}>
              <div style={{ fontWeight: 700, fontSize: '14px', color: 'var(--text-strong)' }}>{stats.value.userTurns}</div>
              <div style={{ fontSize: '10.5px', color: 'var(--text-dim)' }}>对话轮数</div>
            </div>
            <div style={{ background: 'var(--bg)', padding: '6px', borderRadius: '4px', border: '1px solid var(--border-subtle)' }}>
              <div style={{ fontWeight: 700, fontSize: '14px', color: 'var(--text-strong)' }}>{stats.value.toolCalls}</div>
              <div style={{ fontSize: '10.5px', color: 'var(--text-dim)' }}>工具调用</div>
            </div>
            <div style={{ background: 'var(--bg)', padding: '6px', borderRadius: '4px', border: '1px solid var(--border-subtle)' }}>
              <div style={{ fontWeight: 700, fontSize: '14px', color: 'var(--accent-text)' }}>{memories.value.length}</div>
              <div style={{ fontSize: '10.5px', color: 'var(--text-dim)' }}>活跃记忆</div>
            </div>
          </div>
        </div>

        {/* 2. 本轮注入给模型的记忆 Prompt 块 */}
        <div style={cardBox}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '6px' }}>
            <strong style={{ fontSize: '12px' }}>🧠 本轮记忆注入预览</strong>
            <button type="button" style={btnSmall(true)} onClick={distillNow} disabled={distilling.value}>
              {distilling.value ? '提炼中…' : '✨ 提炼当前会话'}
            </button>
          </div>
          {promptPreview.value ? (
            <pre style={{ margin: 0, maxHeight: '120px', overflow: 'auto', padding: '7px', borderRadius: '4px', background: 'var(--bg)', border: '1px solid var(--border-subtle)', fontSize: '11px', whiteSpace: 'pre-wrap', color: 'var(--text)' }}>
              {promptPreview.value}
            </pre>
          ) : (
            <div style={{ color: 'var(--text-dim)', fontSize: '11px' }}>当前会话暂无自动注入的记忆上下文。</div>
          )}
        </div>

        {/* 3. 活跃记忆列表 + 一键 👍/👎 反馈 */}
        <div style={cardBox}>
          <div style={{ fontWeight: 600, marginBottom: '6px' }}>活跃记忆库（即时权重反馈）</div>
          {memories.value.length === 0 ? (
            <div style={{ color: 'var(--text-dim)', fontSize: '11px' }}>暂无记忆条目</div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
              {memories.value.map((m: any) => {
                const id = String(m.id || '')
                const text = String(m.content || m.fact || m.summary || id)
                return (
                  <div key={id} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '6px', padding: '5px 7px', borderRadius: '4px', background: 'var(--bg)', border: '1px solid var(--border-subtle)' }}>
                    <div style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: '11.5px' }} title={text}>
                      {text}
                    </div>
                    <div style={{ display: 'flex', gap: '4px', flexShrink: 0 }}>
                      <button type="button" style={btnSmall()} title="强化该记忆" onClick={() => void feedback(id, 'positive')}>👍</button>
                      <button type="button" style={btnSmall()} title="降低该记忆权重" onClick={() => void feedback(id, 'negative')}>👎</button>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </div>

        {/* 4. 已挂载 Skills 速览 */}
        <div style={cardBox}>
          <div style={{ fontWeight: 600, marginBottom: '6px' }}>可用 Skills ({skills.value.length})</div>
          {skills.value.length === 0 ? (
            <div style={{ color: 'var(--text-dim)', fontSize: '11px' }}>暂无已加载 Skill</div>
          ) : (
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '5px' }}>
              {skills.value.map((s: any) => {
                const name = String(s.name || s.id || '')
                return (
                  <button
                    key={name}
                    type="button"
                    style={btnSmall()}
                    title={String(s.description || '点击在对话中使用该 Skill')}
                    onClick={() => props.onSendToAgent?.(`请结合 Skill \`${name}\` 处理当前任务。`)}
                  >
                    🧩 {name}
                  </button>
                )
              })}
            </div>
          )}
        </div>
      </div>
    )
  },
})
