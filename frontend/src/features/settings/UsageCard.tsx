import { computed, defineComponent, onMounted, ref } from 'vue'
import { BarChart3, RefreshCw } from 'lucide-vue-next'
import { wailsCall } from '../../shared/wails'
import type { UsageSummary } from '../../shared/ipc/types'
import { cacheHint, dayBars, formatPercent, formatTokens, rangeLabel, USAGE_RANGES } from './usageModel'
import './usage-diagnostics.css'

// 设置 / 用量：近几天的模型调用量，按模型、按天。数据来自每次模型调用时服务商返回的 usage，
// 保存在本机数据目录，不上传。
export default defineComponent({
  name: 'UsageCard',
  setup() {
    const days = ref<number>(7)
    const loading = ref(false)
    const error = ref('')
    const summary = ref<UsageSummary | null>(null)

    const load = async () => {
      loading.value = true
      error.value = ''
      try {
        const res = await wailsCall('GetUsageSummary', days.value)
        if (res?.success === false || !res?.totals) {
          summary.value = null
          error.value = String(res?.error || '读取用量失败')
        } else {
          summary.value = res as unknown as UsageSummary
        }
      } catch (e) {
        summary.value = null
        error.value = e instanceof Error ? e.message : String(e)
      } finally {
        loading.value = false
      }
    }
    const pick = (n: number) => {
      if (days.value === n) return
      days.value = n
      void load()
    }
    onMounted(load)

    const bars = computed(() => dayBars(summary.value?.by_day ?? []))
    const empty = computed(() => !summary.value || summary.value.totals.calls === 0)
    // 只有一天时柱状图没有意义，直接看数字。
    const showChart = computed(() => bars.value.length > 1 && !empty.value)

    const tile = (label: string, value: string, note?: string) => (
      <div class="usage-tile"><span>{label}</span><strong>{value}</strong>{note && <small>{note}</small>}</div>
    )

    return () => {
      const s = summary.value
      return <section class="settings-surface usage-card">
        <div class="settings-surface-heading">
          <div><h3>模型用量</h3><p>按服务商返回的 Token 统计，帮你看清哪个模型用得多、缓存有没有起作用。数据只保存在本机。</p></div>
          <button class="settings-icon-button" disabled={loading.value} aria-label="刷新用量" title="刷新用量" onClick={load}><RefreshCw size={15} class={loading.value ? 'settings-spin' : ''} /></button>
        </div>
        <div class="usage-ranges" role="group" aria-label="统计区间">
          {USAGE_RANGES.map(r => <button key={r.days} class={days.value === r.days ? 'active' : ''} aria-pressed={days.value === r.days} disabled={loading.value && days.value !== r.days} onClick={() => pick(r.days)}>{r.label}</button>)}
        </div>
        {error.value && <div class="settings-feedback error" role="status">{error.value}</div>}
        {!error.value && loading.value && !s && <div class="usage-empty">正在读取用量…</div>}
        {s && empty.value && <div class="usage-empty"><BarChart3 size={24} /><strong>{rangeLabel(days.value)}还没有模型调用记录</strong><span>发起一次对话后，这里会出现调用次数和 Token 用量。</span></div>}
        {s && !empty.value && <>
          <div class="usage-tiles">
            {tile('调用次数', formatTokens(s.totals.calls))}
            {tile('输入 Token', formatTokens(s.totals.prompt_tokens))}
            {tile('缓存命中', formatPercent(s.totals.cache_hit_rate, s.totals.prompt_tokens > 0), s.totals.prompt_tokens > 0 ? `${formatTokens(s.totals.cached_tokens)} 命中` : undefined)}
            {tile('输出 Token', formatTokens(s.totals.completion_tokens))}
            {tile('其中推理', formatTokens(s.totals.reasoning_tokens), '含在输出内')}
          </div>
          <p class="usage-hint">{cacheHint(s.totals.prompt_tokens, s.totals.cached_tokens)}</p>
          {showChart.value && <div class="usage-chart" role="img" aria-label={`${rangeLabel(days.value)}每天的 Token 用量`}>
            {bars.value.map(b => <div class="usage-bar" key={b.date} title={`${b.date}：${formatTokens(b.total)} Token，${b.calls} 次调用`}>
              <span class="usage-bar-track"><span class="usage-bar-fill" style={{ height: `${Math.round(b.height * 100)}%` }} /></span>
              <em>{b.label}</em>
            </div>)}
          </div>}
          <table class="usage-table">
            <thead><tr><th>模型</th><th>调用</th><th>输入</th><th>缓存命中</th><th>输出</th><th>合计</th></tr></thead>
            <tbody>{s.by_model.map(m => <tr key={m.model}>
              <td title={m.model}>{m.model || '未命名模型'}</td>
              <td>{formatTokens(m.calls)}</td>
              <td>{formatTokens(m.prompt_tokens)}</td>
              <td>{formatPercent(m.cache_hit_rate, m.prompt_tokens > 0)}</td>
              <td>{formatTokens(m.completion_tokens)}</td>
              <td>{formatTokens(m.total_tokens)}</td>
            </tr>)}</tbody>
          </table>
        </>}
        <footer class="usage-foot">调用次数包含记忆整理、工作流节点等后台调用。没有返回用量的模型服务会按 0 统计。「合计」是输入加输出；推理 Token 已包含在输出里。</footer>
      </section>
    }
  },
})
