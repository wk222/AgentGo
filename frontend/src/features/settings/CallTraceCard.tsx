import { defineComponent, onMounted, ref } from 'vue'
import { wailsCall } from '../../shared/wails'
import { copyToClipboard } from '../../shared/clipboard'
import { notify } from '../../shared/ui/notify'
import type { CallTraceEvent } from '../../shared/ipc/types'
import { STAGE_LABEL, buildTraceView, stepSummary, type TracedCall } from './callTraceModel'
import './call-trace.css'

const shortId = (s: string) => (s.length > 14 ? `${s.slice(0, 6)}…${s.slice(-6)}` : s)
const clock = (ms: number) => new Date(ms).toLocaleTimeString('zh-CN', { hour12: false })

// 设置 / 调用追踪：一次工具调用从模型写的参数到结果的每一步，加上上下文压缩和用量。默认关闭。
export default defineComponent({
  name: 'CallTraceCard',
  setup() {
    const enabled = ref(false)
    const sessions = ref<string[]>([])
    const sid = ref('')
    const events = ref<CallTraceEvent[]>([])
    const open = ref<Record<string, boolean>>({})
    const busy = ref(false)

    const load = async () => {
      busy.value = true
      try {
        const r = await wailsCall('GetCallTrace', sid.value, '', '')
        enabled.value = r?.enabled === true
        sessions.value = (r?.sessions as string[]) || []
        if (!sid.value && sessions.value.length) {
          sid.value = sessions.value[0]
          const again = await wailsCall('GetCallTrace', sid.value, '', '')
          events.value = (again?.events as CallTraceEvent[]) || []
        } else {
          events.value = (r?.events as CallTraceEvent[]) || []
        }
      } catch (e) {
        notify.error(String((e as Error)?.message || e))
      } finally {
        busy.value = false
      }
    }
    onMounted(load)

    const toggle = async () => {
      const r = await wailsCall('SetCallTrace', !enabled.value)
      enabled.value = r?.enabled === true
      notify.success(enabled.value ? '已开启：之后的工具调用会被记录' : '已关闭（已记录的保留）')
    }
    const pick = async (id: string) => {
      sid.value = id
      await load()
    }
    const clear = async () => {
      await wailsCall('ClearCallTrace', sid.value)
      sid.value = ''
      events.value = []
      await load()
    }
    const copyCall = async (c: TracedCall, run: CallTraceEvent[]) => {
      const ok = await copyToClipboard(JSON.stringify({ call: c.steps, run_level: run }, null, 2))
      if (ok) notify.success('已复制这次调用的完整记录')
      else notify.error('复制失败')
    }

    return () => {
      const view = buildTraceView(events.value)
      return <section class="settings-surface trace-card">
        <div class="settings-surface-heading"><div><h3>调用追踪</h3><p>排查“为什么这样答”：看一次工具调用的原始参数、修复后参数、策略决定、结果，以及当时的上下文压缩和 token 用量。密钥已隐藏，只留在内存里，默认关闭。</p></div></div>
        <div class="trace-bar">
          <button class={enabled.value ? 'settings-secondary' : 'settings-primary'} onClick={toggle}>{enabled.value ? '关闭追踪' : '开启追踪'}</button>
          <button class="settings-secondary" disabled={busy.value} onClick={load}>刷新</button>
          {sid.value && <button class="settings-secondary" onClick={clear}>清空此会话</button>}
          <span class={['trace-state', enabled.value && 'on']}>{enabled.value ? '记录中' : '未开启'}</span>
        </div>
        {sessions.value.length > 0 && <div class="trace-sessions">
          {sessions.value.map(id => <button key={id} class={['trace-chip', id === sid.value && 'active']} title={id} onClick={() => pick(id)}>{shortId(id)}</button>)}
        </div>}
        {!enabled.value && !sessions.value.length && <div class="trace-none">开启后再发一条会调用工具的消息，这里就会出现记录。</div>}
        {enabled.value && !view.calls.length && <div class="trace-none">还没有记录。发一条会调用工具的消息后点“刷新”。</div>}
        <ul class="trace-list">
          {view.calls.map(c => {
            const key = `${c.runId}|${c.callId}`
            const run = view.runSteps[c.runId] || []
            const isOpen = open.value[key] === true
            return <li key={key} class={['trace-call', c.failed && 'failed']}>
              <button class="trace-head" aria-expanded={isOpen} onClick={() => (open.value = { ...open.value, [key]: !isOpen })}>
                <strong>{c.tool || '(未知工具)'}</strong>
                <span class="trace-meta">{clock(c.at)} · {c.failed ? '出问题' : '正常'}{c.tookMs ? ` · ${c.tookMs} ms` : ''}</span>
                <code title={`run ${c.runId} / call ${c.callId}`}>{shortId(c.callId)}</code>
              </button>
              {isOpen && <div class="trace-body">
                {[...c.steps, ...run].map(e => <div key={e.seq} class="trace-step">
                  <div class="trace-step-head"><b>{STAGE_LABEL[e.stage] || e.stage}</b><span>{stepSummary(e)}</span></div>
                  {e.data && Object.keys(e.data).length > 0 && <pre class="trace-pre">{JSON.stringify(e.data, null, 2)}</pre>}
                </div>)}
                <div class="trace-actions"><button class="settings-secondary" onClick={() => copyCall(c, run)}>复制完整记录</button></div>
              </div>}
            </li>
          })}
        </ul>
      </section>
    }
  },
})
