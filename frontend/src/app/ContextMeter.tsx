import { computed, defineComponent, onBeforeUnmount, ref, watch } from 'vue'
import { Gauge } from 'lucide-vue-next'
import { wailsCall } from '../shared/wails'
import type { ContextReport } from '../shared/ipc/types'
import './context-meter.css'

const SOURCE_LABELS: Record<string, string> = {
  task_state: '\u4efb\u52a1\u72b6\u6001',
  system_rules: '系统与规则',
  durable_tasks: '持久任务',
  memory: '记忆摘要',
  recalled_memory: '召回记忆',
  workflow: '工作流',
  workspace: '工作区近况',
  hygiene: '上下文提示',
  team_memory: '团队记忆',
  isolation: '隔离说明',
  tool_output: '工具输出',
  history: '对话历史',
  message_protocol_and_media: '消息协议与媒体',
  tool_schemas: '工具定义',
  projection_markup: '投影格式',
  protected_reminders: '保护性提醒',
}
const label = (source: string) => SOURCE_LABELS[source] || source
const fmt = (n: number) => Math.round(n).toLocaleString('en-US')

/** Status-bar gauge for the session's context usage. Refreshes on session change,
 *  when a run finishes, and when opened; it never polls. */
export default defineComponent({
  name: 'ContextMeter',
  props: {
    sessionId: { type: String, default: '' },
    sending: { type: Boolean, default: false },
  },
  setup(props) {
    const report = ref<ContextReport | null>(null)
    const open = ref(false)
    let seq = 0

    const load = async () => {
      const sid = props.sessionId
      const mine = ++seq
      if (!sid) { report.value = null; return }
      try {
        const reply = await wailsCall('GetContextReport', sid)
        if (mine !== seq) return // a newer request or session switch superseded this one
        report.value = reply?.available && reply.report ? reply.report : null
      } catch {
        if (mine === seq) report.value = null
      }
    }

    watch(() => props.sessionId, () => { open.value = false; void load() }, { immediate: true })
    watch(() => props.sending, (now, before) => { if (before && !now) void load() })
    watch(open, isOpen => { if (isOpen) void load() })

    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') open.value = false }
    watch(open, isOpen => {
      if (isOpen) window.addEventListener('keydown', onKey)
      else window.removeEventListener('keydown', onKey)
    })
    onBeforeUnmount(() => { seq++; window.removeEventListener('keydown', onKey) })

    const percent = computed(() => Math.min(999, Math.round(report.value?.window_percent ?? 0)))
    const level = computed(() => (percent.value >= 90 ? 'danger' : percent.value >= 80 ? 'warn' : 'ok'))
    const largestTurns = computed(() => [...(report.value?.turns ?? [])].sort((a, b) => b.token_count - a.token_count).slice(0, 5))
    const segments = computed(() => [...(report.value?.segments ?? [])].sort((a, b) => b.token_count - a.token_count))

    return () => {
      const r = report.value
      if (!r) return null
      const windowSize = r.model_context_window || r.budget_limit
      return (
        <span class="ctx-meter">
          <button
            type="button"
            class={['statusbar-item', 'ctx-meter-trigger', `is-${level.value}`]}
            aria-haspopup="dialog"
            aria-expanded={open.value}
            title="查看上下文用量明细"
            onClick={() => { open.value = !open.value }}
          >
            <Gauge size={12} class="statusbar-icon" />
            <span>上下文 {percent.value}%</span>
          </button>
          {open.value && <div class="ctx-meter-backdrop" onClick={() => { open.value = false }} />}
          {open.value && (
            <div class="ctx-meter-panel" role="dialog" aria-label="上下文用量">
              <header class="ctx-meter-head">
                <strong>上下文用量</strong>
                <span class="ctx-meter-total">{fmt(r.total_tokens)} / {fmt(windowSize)} tokens</span>
              </header>
              <div class={['ctx-meter-bar', `is-${level.value}`]} aria-hidden="true"><i style={{ width: `${Math.min(100, percent.value)}%` }} /></div>

              {!!r.advice?.length && (
                <ul class="ctx-meter-advice">{r.advice.map((line, i) => <li key={i}>{line}</li>)}</ul>
              )}

              <section>
                <h5>按类别</h5>
                <ul class="ctx-meter-rows">
                  {segments.value.map(s => (
                    <li key={s.source} title={s.source}>
                      <span class="ctx-meter-name">{label(s.source)}</span>
                      <span class="ctx-meter-num">{fmt(s.token_count)}<small>{s.percentage.toFixed(0)}%</small></span>
                    </li>
                  ))}
                </ul>
              </section>

              {!!r.top_items?.length && (
                <section>
                  <h5>最大的条目</h5>
                  <ul class="ctx-meter-rows">
                    {r.top_items.map((it, i) => (
                      <li key={i} title={`${label(it.source)} · ${it.label}`}>
                        <span class="ctx-meter-name"><em>{label(it.source)}</em>{it.label}</span>
                        <span class="ctx-meter-num">{fmt(it.token_count)}</span>
                      </li>
                    ))}
                  </ul>
                </section>
              )}

              {largestTurns.value.length > 1 && (
                <section>
                  <h5>最大的回合</h5>
                  <ul class="ctx-meter-rows">
                    {largestTurns.value.map(t => (
                      <li key={t.index} title={t.title}>
                        <span class="ctx-meter-name"><em>#{t.index}</em>{t.title || '(无文字)'}</span>
                        <span class="ctx-meter-num">{fmt(t.token_count)}<small>{t.messages} 条</small></span>
                      </li>
                    ))}
                  </ul>
                </section>
              )}
              <footer class="ctx-meter-foot">估算值；实际以模型返回的用量为准。</footer>
            </div>
          )}
        </span>
      )
    }
  },
})
