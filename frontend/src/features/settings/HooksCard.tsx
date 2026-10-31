import { defineComponent, onMounted, ref } from 'vue'
import { Check, Copy, LoaderCircle, RefreshCw } from 'lucide-vue-next'
import { wailsCall } from '../../shared/wails'
import { copyToClipboard } from '../../shared/clipboard'
import { notify } from '../../shared/ui/notify'
import type { HooksStatus } from '../../shared/ipc/types'
import {
  HOOKS_EXAMPLE, eventLabel, formatClock, formatDuration, groupHooks, hooksSummary, matcherLabel, onFailureLabel, resultLabel, resultTone, shortHash,
} from './hooksModel'
import './hooks-card.css'

/** 最近运行表最多画几行；后端最多留 200 条，全画出来页面会很长。 */
const RECENT_SHOWN = 20

const HELP = [
  '每个钩子是一条命令。运行时，事件的内容（工具名、参数、你的消息等，JSON 格式）会写进这条命令的标准输入。',
  '命令退出码为 0：继续往下做。也可以在退出码 0 时往标准输出打印 {"decision":"block","reason":"原因"} 来拦截。',
  '退出码为 2：拦截。标准错误里的文字就是拦截原因，模型和你都能看到。',
  '其它退出码、起不来、超时：算脚本自己出了问题。默认放行并留一条警告；钩子里写 "onFailure": "block" 就改成拦截。',
  '「收到消息时」的钩子，标准输出里的普通文字会作为补充上下文交给模型；「一轮结束后」不会拖慢界面。',
  '写给脚本的 JSON 是 UTF-8。Windows 上的 Python 默认按 GBK 读标准输入，中文会乱，请用 sys.stdin.buffer.read().decode("utf-8")；脚本输出 UTF-8 或系统默认的 GBK 都能正常显示。',
  '保存 hooks.json 后下次触发时自动生效，不用重启；写错时继续使用上一份有效配置，并在上面提示。',
  '钩子是护栏，不是沙箱：已经同意运行的命令仍可做任何事。只读取数据目录里的这一个文件，工作区里的 hooks.json 不会被执行。',
]

// 设置 / 用户钩子：<数据目录>/hooks.json 里列出的脚本，在工具调用前后、收到消息时、一轮结束后运行。
// 这里只展示状态和最近运行记录，并提供「重新读取」；配置本身仍是手写文件，避免界面里再造一套写法。
export default defineComponent({
  name: 'HooksCard',
  setup() {
    const status = ref<HooksStatus | null>(null)
    const loading = ref(true)
    const reloading = ref(false)
    const error = ref('')
    const copyError = ref('')
    const copied = ref<'path' | 'example' | ''>('')
    let copiedTimer = 0

    const load = async (fresh: boolean): Promise<HooksStatus | null> => {
      try {
        const next = await wailsCall(fresh ? 'ReloadHooks' : 'GetHooksStatus')
        if (!next || typeof next !== 'object' || !Array.isArray(next.hooks)) throw new Error('后端没有返回钩子状态，可能需要更新到新版本。')
        status.value = next
        error.value = ''
        return next
      } catch (e) {
        error.value = e instanceof Error ? e.message : String(e)
        return null
      } finally {
        loading.value = false
      }
    }

    onMounted(() => { void load(false) })

    const reload = async () => {
      if (reloading.value) return
      reloading.value = true
      const next = await load(true)
      reloading.value = false
      if (next) notify.success(next.total > 0 ? `已重新读取：${next.total} 个钩子在起作用` : '已重新读取：目前没有钩子在起作用')
    }

    const copy = async (what: 'path' | 'example', text: string) => {
      copyError.value = ''
      const ok = await copyToClipboard(text)
      window.clearTimeout(copiedTimer)
      if (!ok) {
        copied.value = ''
        copyError.value = '无法写入剪贴板，请手动选中后复制。'
        return
      }
      copied.value = what
      copiedTimer = window.setTimeout(() => { copied.value = '' }, 1800)
    }

    const copyButton = (what: 'path' | 'example', text: string, label: string) =>
      <button type="button" class="hooks-copy" aria-label={label} title={label} onClick={() => void copy(what, text)}>
        {copied.value === what ? <Check size={13} /> : <Copy size={13} />}
      </button>

    return () => {
      const s = status.value
      const summary = s ? hooksSummary(s) : null
      return <section class="settings-surface hooks-card">
        <div class="settings-surface-heading">
          <div><h3>用户钩子</h3><p>在智能体调用工具前后、收到你的消息时、答完一轮后，自动运行你自己的脚本：拦截危险命令、给消息补充上下文、留审计记录。</p></div>
          <button class="settings-secondary hooks-reload" disabled={reloading.value || loading.value} onClick={() => void reload()}>
            {reloading.value ? <LoaderCircle size={14} class="settings-spin" /> : <RefreshCw size={14} />}重新读取
          </button>
        </div>
        {error.value && <div class="settings-feedback error" role="status">{error.value}</div>}
        {copyError.value && <div class="settings-feedback error" role="status">{copyError.value}</div>}
        {loading.value && !s && <div class="hooks-none">正在读取…</div>}
        {s && summary && <>
          <div class={['hooks-state', summary.tone]} role="status"><i aria-hidden="true" /><strong>{summary.text}</strong></div>
          <dl class="hooks-meta">
            <div class="wide"><dt>配置文件</dt><dd><code title={s.path}>{s.path}</code>{s.path && copyButton('path', s.path, '复制路径')}</dd></div>
            {s.exists && <div><dt>最近读取</dt><dd>{formatClock(s.loaded_at)}</dd></div>}
            {s.exists && <div><dt>内容校验</dt><dd><code>{shortHash(s.sha256)}</code></dd></div>}
          </dl>
          {s.error && <div class="settings-feedback error" role="status"><span><strong>配置文件读取失败：</strong>{s.error}</span></div>}
          {s.problems.length > 0 && <div class="hooks-problems" role="status">
            <strong>下面这些写法不能用，已经跳过（其余钩子照常工作）</strong>
            <ul>{s.problems.map((p, i) => <li key={`${p.where}-${i}`}><code>{p.where}</code><span>{p.message}</span></li>)}</ul>
          </div>}

          <div class="hooks-groups">
            {groupHooks(s.hooks).map(g => <div class="hooks-group" key={g.id}>
              <div class="hooks-group-head"><strong>{g.label}</strong><span>{g.hooks.length > 0 ? `${g.hooks.length} 个` : '没有配置'}</span></div>
              <p>{g.hint}</p>
              {g.hooks.length > 0 && <ul>{g.hooks.map((h, i) => <li key={`${g.id}-${i}`}>
                <code class="hooks-cmd" title={h.command}>{h.command}</code>
                <span class="hooks-tags">
                  {matcherLabel(h.event, h.matcher) && <em>{matcherLabel(h.event, h.matcher)}</em>}
                  <em>{h.timeout_s} 秒超时</em>
                  <em class={h.on_failure === 'block' ? 'warn' : ''}>{onFailureLabel(h.on_failure)}</em>
                </span>
              </li>)}</ul>}
            </div>)}
          </div>

          <div class="hooks-recent">
            <h4>最近运行<small>只保留本次启动后的记录</small></h4>
            {s.recent.length === 0
              ? <div class="hooks-none">还没有钩子运行过。</div>
              : <div class="hooks-table-wrap"><table class="hooks-table">
                <thead><tr><th>时间</th><th>时机</th><th>工具</th><th>结果</th><th>耗时</th><th>说明</th></tr></thead>
                <tbody>{s.recent.slice(0, RECENT_SHOWN).map(r => <tr key={r.seq}>
                  <td>{formatClock(r.at)}</td>
                  <td>{eventLabel(r.event)}</td>
                  <td>{r.tool || '—'}</td>
                  <td><span class={['hooks-result', resultTone(r.result)]}>{resultLabel(r.result)}</span></td>
                  <td>{formatDuration(r.duration_ms)}</td>
                  <td class="hooks-reason" title={r.reason || r.hook}>{r.reason || <span class="hooks-hook">{r.hook}</span>}</td>
                </tr>)}</tbody>
              </table></div>}
            {s.recent.length > RECENT_SHOWN && <div class="hooks-more">只显示最新 {RECENT_SHOWN} 条，共 {s.recent.length} 条。</div>}
          </div>

          <details class="hooks-help">
            <summary>怎么写 hooks.json</summary>
            <ul>{HELP.map(t => <li key={t}>{t}</li>)}</ul>
            <div class="hooks-example-head"><strong>示例：拦截危险命令，并在每轮结束后通知</strong>{copyButton('example', HOOKS_EXAMPLE, '复制示例')}</div>
            <pre class="hooks-example">{HOOKS_EXAMPLE}</pre>
            <p class="hooks-note">工具名 Bash、Read、Write、Edit、MultiEdit、Glob、Grep、LS、WebSearch 可以直接写，会对应到 AgentGo 里的同类工具；匹配范围写「Bash|Edit」表示其中任意一个，不写就是所有工具。</p>
          </details>
        </>}
      </section>
    }
  },
})
