import { defineComponent, ref, computed, onMounted, onUnmounted } from 'vue'
import { wailsCall } from '../../shared/wails'
import type { BackgroundTask } from '../../shared/ipc/types'
import { sessionIdOf, sessionTitle } from '../../stores/useChat'
import { useLayout } from '../../stores/useLayout'
import { useRunningWork } from '../../stores/useRunningWork'
import { isInterrupted, taskInputSummary, taskKindLabel, taskStatusText, taskStatusTone } from '../../stores/runningWorkModel'
import { useChatContext } from '../chat/chatContext'

interface EngineRunItem {
  /** 后端的 engine.ActiveRun 叫 run_id。 */
  run_id: string
  engine: string
  session_id: string
  started_at: number
}

interface TrackedRunItem {
  session_id: string
  session_title?: string
  phase: string
  detail: string
  elapsed_sec: number
  idle_sec: number
  stream_ack: boolean
  chunk_count: number
  trace?: string[]
  error?: string
  hint?: string
  is_running?: boolean
  started_at: number
  updated_at: number
}

interface TerminalItem {
  id: string
  title: string
  cwd: string
  shell: string
  cols: number
  rows: number
  running: boolean
  started_at: string
  updated_at: string
}

interface ScheduledJobItem {
  id: string
  title: string
  spec: string
  prompt: string
  session_id?: string
  next_run_at: number
  interval_sec: number
  enabled: boolean
  last_run_at?: number
  created_at?: number
}

const CRON_PRESETS = [
  { label: '每 30 分钟', spec: '每30分钟', desc: '每隔 30 分钟自动唤醒一次' },
  { label: '每 1 小时', spec: 'every 1h', desc: '每隔 60 分钟自动唤醒一次' },
  { label: '每天 09:00', spec: 'cron:0 9 * * *', desc: '每天早上 9:00 定时唤醒' },
  { label: '每天 18:00', spec: 'cron:0 18 * * *', desc: '每天傍晚 18:00 定时复盘' },
  { label: '每周一 09:30', spec: 'cron:30 9 * * 1', desc: '每周一早会前自动汇总' },
]

export const TasksPanel = defineComponent({
  name: 'TasksPanel',
  setup() {
    // 必须用 AppShell 提供的那份对话状态：自己 useChat() 出来的是一份空的、与主界面无关的状态，
    // 会话列表是空的，"跳转对话"也只切了这份假状态。
    const chatCtx = useChatContext()
    const sessions = computed(() => chatCtx.sessions)
    const currentSessionId = computed(() => chatCtx.sessionId)
    const selectSession = (id: string) => chatCtx.selectSession(id)
    const layout = useLayout()
    const running = useRunningWork()
    /** 本程序此刻真在跑的后台任务；库里写着 running 但不在这里的，是上次被打断的。 */
    const liveTaskIds = computed(() => new Set(running.work.value.items.map(i => i.task_id).filter((id): id is string => !!id)))

    const activeSection = ref<'threads' | 'cron' | 'queue'>('threads')
    const autoPoll = ref(true)
    const loading = ref(false)
    const toastMsg = ref('')

    // Runtime & Thread state
    const goroutines = ref(0)
    const allocMb = ref(0)
    const sysMb = ref(0)
    const activeAgentRuns = ref(0)
    const engineRuns = ref<EngineRunItem[]>([])
    const trackedRuns = ref<TrackedRunItem[]>([])
    const terminals = ref<TerminalItem[]>([])
    const steerInputs = ref<Record<string, string>>({})

    // Cron / Auto-wake state
    const cronJobs = ref<ScheduledJobItem[]>([])
    const cronTitle = ref('')
    const cronSpec = ref('cron:0 9 * * *')
    const cronPrompt = ref('')
    const cronSessionId = ref('')

    // Background & Admin Task Queue state
    const bgTasks = ref<BackgroundTask[]>([])
    const adminTasks = ref<any[]>([])
    const quickBgPrompt = ref('')

    let pollTimer: ReturnType<typeof setInterval> | null = null

    function showToast(msg: string) {
      toastMsg.value = msg
      setTimeout(() => {
        if (toastMsg.value === msg) toastMsg.value = ''
      }, 2800)
    }

    async function loadAll() {
      loading.value = true
      try {
        const [rtOverview, jobs, bg, adm] = await Promise.all([
          wailsCall('GetRuntimeThreadOverview').catch(() => null),
          wailsCall('ListScheduledJobs').catch(() => []),
          wailsCall('ListBackgroundTasks', 50).catch(() => []),
          wailsCall('ListAdminTasks', 50, 0).catch(() => []),
        ])

        if (rtOverview && typeof rtOverview === 'object') {
          goroutines.value = Number(rtOverview.goroutines || 0)
          allocMb.value = Number(rtOverview.alloc_mb || 0)
          sysMb.value = Number(rtOverview.sys_mb || 0)
          activeAgentRuns.value = Number(rtOverview.active_agent_runs || 0)
          engineRuns.value = Array.isArray(rtOverview.engine_runs) ? rtOverview.engine_runs : []
          trackedRuns.value = Array.isArray(rtOverview.tracked_runs)
            ? [...rtOverview.tracked_runs].sort((a, b) => (b.updated_at || 0) - (a.updated_at || 0))
            : []
          terminals.value = Array.isArray(rtOverview.terminals) ? rtOverview.terminals : []
        }

        cronJobs.value = Array.isArray(jobs) ? jobs : []
        bgTasks.value = Array.isArray(bg) ? bg : []
        adminTasks.value = Array.isArray(adm) ? adm : []
      } finally {
        loading.value = false
      }
    }

    onMounted(() => {
      loadAll()
      pollTimer = setInterval(() => {
        if (autoPoll.value) loadAll()
      }, 3000)
    })

    onUnmounted(() => {
      if (pollTimer) clearInterval(pollTimer)
    })

    // Computed counts
    const runningThreadCount = computed(() => {
      const runningTracked = trackedRuns.value.filter(
        r => r.is_running || (r.phase !== 'done' && r.phase !== 'cancelled' && r.phase !== 'error' && r.phase !== 'idle')
      ).length
      // 后端汇总的在跑数（会话、引擎运行、后台任务）才是全的；上面几项只覆盖界面发起的那部分。
      return Math.max(activeAgentRuns.value, engineRuns.value.length, runningTracked, running.work.value.running)
    })

    const enabledCronCount = computed(() => cronJobs.value.filter(j => j.enabled).length)
    const runningTerminalsCount = computed(() => terminals.value.filter(t => t.running).length)

    // Thread Actions
    async function killSessionThread(sessionId: string) {
      if (!sessionId) return
      await Promise.all([
        wailsCall('CancelEngineSession', sessionId).catch(() => {}),
        wailsCall('CancelAgentRun', sessionId).catch(() => {}),
      ])
      showToast(`已发送强制中断指令 (${sessionId.slice(0, 8)})`)
      await loadAll()
    }

    async function cancelEngineRun(runId: string) {
      if (!runId) return
      await wailsCall('CancelEngineRun', runId).catch(() => {})
      showToast('已发送强制中断指令')
      await loadAll()
    }

    async function steerSessionThread(sessionId: string) {
      const text = (steerInputs.value[sessionId] || '').trim()
      if (!sessionId || !text) return
      await wailsCall('SteerSession', sessionId, text)
      steerInputs.value[sessionId] = ''
      showToast('已向运行中线程注入插话纠偏指令')
      await loadAll()
    }

    async function clearFinishedThreads() {
      const res = await wailsCall('ClearFinishedTrackedRuns')
      showToast(`已清理 ${res?.cleared ?? 0} 条已结束线程记录`)
      await loadAll()
    }

    function jumpToSession(sessionId: string) {
      if (!sessionId) return
      selectSession(sessionId)
      layout.focusAgent()
    }

    async function cancelBgTask(task: BackgroundTask) {
      const res = await wailsCall('CancelBackgroundTask', task.id).catch((e: any) => ({ success: false, error: e?.message || String(e) }))
      showToast(res?.success ? '已请求取消这个任务' : (res?.error || '没能取消这个任务'))
      void running.refresh()
      await loadAll()
    }

    async function closeTerminal(id: string) {
      await wailsCall('TerminalClose', id)
      showToast(`已关闭终端进程 ${id}`)
      await loadAll()
    }

    // Cron / Scheduled Auto-Wake Actions
    async function createCronJob() {
      const prompt = cronPrompt.value.trim()
      const spec = cronSpec.value.trim()
      if (!prompt || !spec) {
        showToast('请填写唤醒规则与执行指令 Prompt')
        return
      }
      const title = cronTitle.value.trim() || prompt.slice(0, 24)
      const targetSid = cronSessionId.value || currentSessionId.value || ''
      const res = await wailsCall('CreateScheduledJob', title, spec, prompt, targetSid)
      if (res?.success) {
        cronTitle.value = ''
        cronPrompt.value = ''
        showToast(`定时唤醒任务「${title}」已创建`)
        await loadAll()
      } else {
        showToast(res?.error || '创建定时任务失败')
      }
    }

    async function toggleCronJob(job: ScheduledJobItem) {
      const nextState = !job.enabled
      const res = await wailsCall('ToggleScheduledJob', job.id, nextState)
      if (res?.success) {
        job.enabled = nextState
        showToast(nextState ? `已启用「${job.title}」定时唤醒` : `已暂停「${job.title}」（不消耗 Token）`)
        await loadAll()
      }
    }

    async function triggerCronNow(job: ScheduledJobItem) {
      const res = await wailsCall('TriggerScheduledJobNow', job.id)
      if (res?.success) {
        showToast(`已立即唤醒执行「${job.title}」`)
        await loadAll()
      } else {
        showToast(res?.error || '唤醒失败')
      }
    }

    async function removeCronJob(id: string) {
      await wailsCall('DeleteScheduledJob', id)
      showToast('定时任务已删除')
      await loadAll()
    }

    // Background & Admin Task Actions
    async function dispatchQuickBgTask() {
      const p = quickBgPrompt.value.trim()
      if (!p) return
      await wailsCall('StartBackgroundTask', 'manual', currentSessionId.value || '', p)
      quickBgPrompt.value = ''
      showToast('后台异步任务已派发至 TaskHub')
      await loadAll()
    }

    async function handleAdminAction(action: 'PauseAdminTask' | 'ResumeAdminTask' | 'CancelAdminTask' | 'RetryAdminTask', taskId: string) {
      await wailsCall(action, taskId)
      showToast(`已执行任务操作: ${action.replace('AdminTask', '')}`)
      await loadAll()
    }

    function formatCountdown(unixSec: number, enabled: boolean): string {
      if (!enabled) return '已暂停（零 Token 消耗）'
      if (!unixSec) return '未排期'
      const diff = unixSec - Math.floor(Date.now() / 1000)
      const timeStr = new Date(unixSec * 1000).toLocaleString('zh-CN', { hour12: false })
      if (diff <= 0) return `即将唤醒 (${timeStr})`
      if (diff < 60) return `${diff} 秒后唤醒 (${timeStr})`
      if (diff < 3600) return `${Math.floor(diff / 60)} 分钟后 (${timeStr})`
      return `${(diff / 3600).toFixed(1)} 小时后 (${timeStr})`
    }

    function phaseStatusClass(phase: string, isRunning?: boolean): string {
      if (isRunning || phase === 'thinking' || phase === 'tool_call' || phase === 'streaming' || phase === 'starting') {
        return 'running'
      }
      if (phase === 'done') return 'done'
      if (phase === 'error' || phase === 'cancelled') return 'error'
      return 'idle'
    }

    return () => (
      <div class="tm-center">
        {/* Top Header & Real-time Telemetry Strip */}
        <header class="tm-header">
          <div class="tm-header-left">
            <h2 class="tm-title">任务与线程管理中心</h2>
            <span class="tm-subtitle">
              实时监控 Agent 运行中协程线程、定时自动唤醒 (Cron) 与后台异步任务队列
            </span>
          </div>
          <div class="tm-header-actions">
            {toastMsg.value && <span class="tm-toast">{toastMsg.value}</span>}
            <label class="tm-poll-toggle" title="每 3 秒自动同步后端协程与任务状态">
              <input
                type="checkbox"
                checked={autoPoll.value}
                onChange={(e: Event) => { autoPoll.value = (e.target as HTMLInputElement).checked }}
              />
              <span class={['tm-live-dot', autoPoll.value ? 'on' : '']} />
              <span>实时监控 (3s)</span>
            </label>
            <button class="tm-btn" onClick={loadAll} disabled={loading.value}>
              {loading.value ? '同步中…' : '🔄 立即刷新'}
            </button>
          </div>
        </header>

        {/* 4 Metric Cards */}
        <div class="tm-kpi-grid">
          <div
            class={['tm-kpi-card', activeSection.value === 'threads' ? 'active' : '']}
            onClick={() => { activeSection.value = 'threads' }}
          >
            <div class="tm-kpi-top">
              <span class="tm-kpi-label">⚡ 活跃会话线程</span>
              <span class={['tm-kpi-pill', runningThreadCount.value > 0 ? 'live' : '']}>
                {runningThreadCount.value > 0 ? '运行中' : '空闲'}
              </span>
            </div>
            <div class="tm-kpi-value">{runningThreadCount.value}</div>
            <div class="tm-kpi-meta">
              跟踪记录 {trackedRuns.value.length} 条 · 活跃终端 {runningTerminalsCount.value} 个
            </div>
          </div>

          <div
            class="tm-kpi-card"
            onClick={() => { activeSection.value = 'threads' }}
          >
            <div class="tm-kpi-top">
              <span class="tm-kpi-label">🧵 Go 运行时协程</span>
              <span class="tm-kpi-pill">Runtime</span>
            </div>
            <div class="tm-kpi-value">{goroutines.value}</div>
            <div class="tm-kpi-meta">
              堆内存 {allocMb.value.toFixed(1)} MB / 系统 {sysMb.value.toFixed(1)} MB
            </div>
          </div>

          <div
            class={['tm-kpi-card', activeSection.value === 'cron' ? 'active' : '']}
            onClick={() => { activeSection.value = 'cron' }}
          >
            <div class="tm-kpi-top">
              <span class="tm-kpi-label">⏰ 定时自动唤醒任务</span>
              <span class={['tm-kpi-pill', enabledCronCount.value > 0 ? 'warn' : '']}>
                {enabledCronCount.value} 启用 / {cronJobs.value.length} 总计
              </span>
            </div>
            <div class="tm-kpi-value">{cronJobs.value.length}</div>
            <div class="tm-kpi-meta">
              支持 Cron 表达式与间隔唤醒，随时一键暂停防消耗 Token
            </div>
          </div>

          <div
            class={['tm-kpi-card', activeSection.value === 'queue' ? 'active' : '']}
            onClick={() => { activeSection.value = 'queue' }}
          >
            <div class="tm-kpi-top">
              <span class="tm-kpi-label">📋 后台与长时契约队列</span>
              <span class="tm-kpi-pill">TaskHub</span>
            </div>
            <div class="tm-kpi-value">{bgTasks.value.length + adminTasks.value.length}</div>
            <div class="tm-kpi-meta">
              异步队列 {bgTasks.value.length} 个 · 契约任务 {adminTasks.value.length} 个
            </div>
          </div>
        </div>

        {/* Section Tabs */}
        <div class="tm-tabs-bar">
          <button
            class={['tm-tab-btn', activeSection.value === 'threads' ? 'active' : '']}
            onClick={() => { activeSection.value = 'threads' }}
          >
            ⚡ 运行中线程与终端管理
            {runningThreadCount.value > 0 && <span class="tm-tab-badge live">{runningThreadCount.value}</span>}
          </button>
          <button
            class={['tm-tab-btn', activeSection.value === 'cron' ? 'active' : '']}
            onClick={() => { activeSection.value = 'cron' }}
          >
            ⏰ 定时自动唤醒任务 (Cron)
            <span class="tm-tab-badge">{enabledCronCount.value}/{cronJobs.value.length}</span>
          </button>
          <button
            class={['tm-tab-btn', activeSection.value === 'queue' ? 'active' : '']}
            onClick={() => { activeSection.value = 'queue' }}
          >
            📋 后台与契约任务队列
            <span class="tm-tab-badge">{bgTasks.value.length + adminTasks.value.length}</span>
          </button>
        </div>

        {/* ══ SECTION 1: ACTIVE THREADS & TERMINALS ══ */}
        {activeSection.value === 'threads' && (
          <div class="tm-body">
            {/* Active Engine Router Runs (if any raw engine run is active) */}
            {engineRuns.value.length > 0 && (
              <div class="tm-section-card">
                <div class="tm-section-head">
                  <div>
                    <h3 class="tm-section-title">🔥 底层引擎活跃执行线程 (Engine Router)</h3>
                    <p class="tm-section-desc">正在占用 LLM 流式连接或工具调用的底层协程</p>
                  </div>
                </div>
                <div class="tm-list">
                  {engineRuns.value.map(er => (
                    <div key={er.run_id} class="tm-thread-card running">
                      <div class="tm-thread-main">
                        <span class="tm-status-dot running" />
                        <span class="tm-thread-title">引擎 [{er.engine}] · 会话 {er.session_id}</span>
                        <span class="tm-badge running">执行中</span>
                      </div>
                      <div class="tm-thread-actions">
                        {er.session_id && (
                          <button class="tm-btn sm" onClick={() => jumpToSession(er.session_id)}>
                            ↗ 跳转对话
                          </button>
                        )}
                        <button class="tm-btn sm danger" onClick={() => (er.session_id ? killSessionThread(er.session_id) : cancelEngineRun(er.run_id))}>
                          ⏹ 强制终止线程
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Tracked Session Runs */}
            <div class="tm-section-card">
              <div class="tm-section-head">
                <div>
                  <h3 class="tm-section-title">⚡ Agent 会话线程状态监控与插话干预</h3>
                  <p class="tm-section-desc">
                    监控每个对话会话的实时阶段（思考 / 工具调用 / 流式输出）、耗时与空闲秒数，支持随时强杀或中途插话纠偏 (Steer)
                  </p>
                </div>
                {trackedRuns.value.length > 0 && (
                  <button class="tm-btn sm" onClick={clearFinishedThreads}>
                    🧹 清理已结束记录
                  </button>
                )}
              </div>

              {trackedRuns.value.length === 0 ? (
                <div class="tm-empty">
                  <div class="tm-empty-title">当前没有活跃或挂起的 Agent 会话线程</div>
                  <div class="tm-empty-sub">在对话工作区发起提问后，可在此实时观测协程阶段、流式分片数并随时中途干预</div>
                </div>
              ) : (
                <div class="tm-list">
                  {trackedRuns.value.map(tr => {
                    const statusCls = phaseStatusClass(tr.phase, tr.is_running)
                    const isLive = statusCls === 'running'
                    return (
                      <div key={tr.session_id} class={['tm-thread-card', statusCls]}>
                        <div class="tm-thread-top">
                          <div class="tm-thread-main">
                            <span class={['tm-status-dot', statusCls]} />
                            <span class="tm-thread-title" title={tr.session_id}>
                              {tr.session_title || `会话 ${tr.session_id.slice(0, 14)}`}
                            </span>
                            <span class={['tm-badge', statusCls]}>
                              {isLive ? `运行中 · ${tr.phase}` : tr.phase}
                            </span>
                            <span class="tm-thread-metrics">
                              耗时 {tr.elapsed_sec}s · 空闲 {tr.idle_sec}s · 分片 {tr.chunk_count}
                            </span>
                          </div>
                          <div class="tm-thread-actions">
                            <button class="tm-btn sm" onClick={() => jumpToSession(tr.session_id)}>
                              ↗ 跳转此对话
                            </button>
                            {isLive && (
                              <button class="tm-btn sm danger" onClick={() => killSessionThread(tr.session_id)}>
                                ⏹ 强制中断 (Kill)
                              </button>
                            )}
                          </div>
                        </div>

                        {(tr.detail || tr.hint || tr.error) && (
                          <div class="tm-thread-detail">
                            {tr.detail && <span>当前动作: <code>{tr.detail}</code></span>}
                            {tr.hint && <span class="tm-thread-hint">💡 {tr.hint}</span>}
                            {tr.error && <span class="tm-thread-err">❌ {tr.error}</span>}
                          </div>
                        )}

                        {Array.isArray(tr.trace) && tr.trace.length > 0 && (
                          <div class="tm-trace-row">
                            {tr.trace.slice(-6).map((step, idx) => (
                              <span key={idx} class="tm-trace-pill">{step}</span>
                            ))}
                          </div>
                        )}

                        {isLive && (
                          <div class="tm-steer-row">
                            <input
                              class="tm-input"
                              placeholder="向该运行中线程插话纠偏 (Steer)，无需中断重来…"
                              value={steerInputs.value[tr.session_id] || ''}
                              onInput={(e: Event) => {
                                steerInputs.value[tr.session_id] = (e.target as HTMLInputElement).value
                              }}
                              onKeydown={(e: KeyboardEvent) => {
                                if (e.key === 'Enter') steerSessionThread(tr.session_id)
                              }}
                            />
                            <button class="tm-btn sm primary" onClick={() => steerSessionThread(tr.session_id)}>
                              ⚡ 插话纠偏
                            </button>
                          </div>
                        )}
                      </div>
                    )
                  })}
                </div>
              )}
            </div>

            {/* Active PTY Terminals */}
            <div class="tm-section-card">
              <div class="tm-section-head">
                <div>
                  <h3 class="tm-section-title">💻 活跃终端进程 (PTY Sessions)</h3>
                  <p class="tm-section-desc">管理后台驻留的 PowerShell / Bash 终端子进程</p>
                </div>
              </div>

              {terminals.value.length === 0 ? (
                <div class="tm-empty compact">暂无驻留的终端进程</div>
              ) : (
                <div class="tm-list">
                  {terminals.value.map(term => (
                    <div key={term.id} class="tm-thread-card">
                      <div class="tm-thread-top">
                        <div class="tm-thread-main">
                          <span class={['tm-status-dot', term.running ? 'running' : 'idle']} />
                          <span class="tm-thread-title">{term.title || term.id}</span>
                          <span class="tm-badge">{term.shell || 'pwsh'}</span>
                          <span class="tm-thread-metrics">{term.cwd} ({term.cols}×{term.rows})</span>
                        </div>
                        <div class="tm-thread-actions">
                          <button class="tm-btn sm danger" onClick={() => closeTerminal(term.id)}>
                            ✕ 终止终端
                          </button>
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        )}

        {/* ══ SECTION 2: SCHEDULED AUTO-WAKE CRON JOBS ══ */}
        {activeSection.value === 'cron' && (
          <div class="tm-body">
            {/* Creator Card */}
            <div class="tm-section-card">
              <div class="tm-section-head">
                <div>
                  <h3 class="tm-section-title">⏰ 新建定时自动唤醒任务 (Scheduled Auto-Wake)</h3>
                  <p class="tm-section-desc">
                    到达指定时间或周期时自动唤醒 Agent 执行预设 Prompt。提示：不需要运行时可随时在下方面板一键暂停，避免后台消耗 Token。
                  </p>
                </div>
              </div>

              <div class="tm-cron-form">
                <div class="tm-form-row">
                  <div class="tm-field">
                    <label>任务名称</label>
                    <input
                      class="tm-input"
                      placeholder="例如：每日代码静态巡检 / Git 状态汇总"
                      value={cronTitle.value}
                      onInput={(e: Event) => { cronTitle.value = (e.target as HTMLInputElement).value }}
                    />
                  </div>
                  <div class="tm-field">
                    <label>唤醒规则 (支持 Cron 或自然周期)</label>
                    <input
                      class="tm-input mono"
                      placeholder="cron:0 9 * * * 或 every 1h / 每30分钟"
                      value={cronSpec.value}
                      onInput={(e: Event) => { cronSpec.value = (e.target as HTMLInputElement).value }}
                    />
                  </div>
                  <div class="tm-field">
                    <label>绑定目标对话 (可选)</label>
                    <select
                      class="tm-input"
                      value={cronSessionId.value}
                      onChange={(e: Event) => { cronSessionId.value = (e.target as HTMLSelectElement).value }}
                    >
                      <option value="">当前活跃对话 / 自动后台会话</option>
                      {sessions.value.slice(0, 30).map(s => (
                        <option key={sessionIdOf(s)} value={sessionIdOf(s)}>
                          {(sessionTitle(s) || sessionIdOf(s)).slice(0, 32)}
                        </option>
                      ))}
                    </select>
                  </div>
                </div>

                {/* Preset Pills */}
                <div class="tm-preset-bar">
                  <span class="tm-preset-label">快捷周期预设：</span>
                  {CRON_PRESETS.map(p => (
                    <button
                      key={p.spec}
                      type="button"
                      class={['tm-preset-pill', cronSpec.value === p.spec ? 'active' : '']}
                      title={p.desc}
                      onClick={() => { cronSpec.value = p.spec }}
                    >
                      {p.label} <code>{p.spec}</code>
                    </button>
                  ))}
                </div>

                <div class="tm-field">
                  <label>唤醒时自动发送给 Agent 的指令 (Prompt)</label>
                  <textarea
                    class="tm-textarea"
                    rows={2}
                    placeholder="输入定时唤醒后让 Agent 自动执行的任务，例如：检查工作区是否有未提交改动并生成简要日报…"
                    value={cronPrompt.value}
                    onInput={(e: Event) => { cronPrompt.value = (e.target as HTMLTextAreaElement).value }}
                  />
                </div>

                <div class="tm-form-footer">
                  <span class="tm-token-tip">
                    💡 节流保护：不需要定时任务时请随手关闭启用开关或删除，防止无人值守时持续消耗模型 Token。
                  </span>
                  <button class="tm-btn primary" onClick={createCronJob}>
                    + 创建定时唤醒任务
                  </button>
                </div>
              </div>
            </div>

            {/* Existing Scheduled Jobs */}
            <div class="tm-section-card">
              <div class="tm-section-head">
                <div>
                  <h3 class="tm-section-title">📅 已配置的定时自动唤醒列表 ({cronJobs.value.length})</h3>
                  <p class="tm-section-desc">支持随时启停开关、立即手动唤醒测试或彻底删除</p>
                </div>
              </div>

              {cronJobs.value.length === 0 ? (
                <div class="tm-empty">
                  <div class="tm-empty-title">暂未配置任何定时唤醒任务</div>
                  <div class="tm-empty-sub">零定时任务运行中，不会产生任何后台自动 Token 消耗</div>
                </div>
              ) : (
                <div class="tm-list">
                  {cronJobs.value.map(j => (
                    <div key={j.id} class={['tm-thread-card', j.enabled ? 'running' : 'idle']}>
                      <div class="tm-thread-top">
                        <div class="tm-thread-main">
                          <span class={['tm-status-dot', j.enabled ? 'running' : 'idle']} />
                          <span class="tm-thread-title">{j.title}</span>
                          <code class="tm-spec-badge">{j.spec}</code>
                          <span class={['tm-badge', j.enabled ? 'running' : 'idle']}>
                            {formatCountdown(j.next_run_at, j.enabled)}
                          </span>
                        </div>
                        <div class="tm-thread-actions">
                          <button
                            class={['tm-btn sm', j.enabled ? '' : 'primary']}
                            onClick={() => toggleCronJob(j)}
                            title={j.enabled ? '暂停后不会再自动触发，零 Token 消耗' : '重新启用定时自动唤醒'}
                          >
                            {j.enabled ? '⏸ 暂停唤醒' : '▶ 启用唤醒'}
                          </button>
                          <button
                            class="tm-btn sm"
                            onClick={() => triggerCronNow(j)}
                            title="不等待定时器，立即派发一次后台唤醒任务"
                          >
                            ⚡ 立即唤醒一次
                          </button>
                          <button class="tm-btn sm danger" onClick={() => removeCronJob(j.id)}>
                            🗑 删除
                          </button>
                        </div>
                      </div>

                      <div class="tm-thread-detail">
                        <span>唤醒指令: <code>{j.prompt}</code></span>
                        {j.last_run_at ? (
                          <span class="tm-thread-metrics">
                            上次唤醒: {new Date(j.last_run_at * 1000).toLocaleString('zh-CN', { hour12: false })}
                          </span>
                        ) : (
                          <span class="tm-thread-metrics">尚未触发过</span>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        )}

        {/* ══ SECTION 3: BACKGROUND TASKHUB & DURABLE ADMIN TASKS ══ */}
        {activeSection.value === 'queue' && (
          <div class="tm-body">
            {/* Quick Background Task Dispatcher */}
            <div class="tm-section-card">
              <div class="tm-section-head">
                <div>
                  <h3 class="tm-section-title">🚀 派发后台异步任务 (Background TaskHub)</h3>
                  <p class="tm-section-desc">不阻塞当前主对话窗口，在独立后台协程中异步执行 Agent 任务</p>
                </div>
              </div>
              <div class="tm-steer-row" style={{ marginTop: '4px' }}>
                <input
                  class="tm-input"
                  placeholder="输入后台异步任务指令，例如：扫描 internal/ 目录整理所有导出的 IPC 方法列表…"
                  value={quickBgPrompt.value}
                  onInput={(e: Event) => { quickBgPrompt.value = (e.target as HTMLInputElement).value }}
                  onKeydown={(e: KeyboardEvent) => {
                    if (e.key === 'Enter') dispatchQuickBgTask()
                  }}
                />
                <button class="tm-btn primary" onClick={dispatchQuickBgTask}>
                  🚀 立即后台运行
                </button>
              </div>
            </div>

            {/* Durable Admin Tasks */}
            {adminTasks.value.length > 0 && (
              <div class="tm-section-card">
                <div class="tm-section-head">
                  <div>
                    <h3 class="tm-section-title">🛡️ 长时契约任务 (Admin Durable Runner)</h3>
                    <p class="tm-section-desc">支持断点暂停、恢复、失败重试与取消的持久化任务</p>
                  </div>
                </div>
                <div class="tm-list">
                  {adminTasks.value.map(at => (
                    <div key={at.id} class="tm-thread-card">
                      <div class="tm-thread-top">
                        <div class="tm-thread-main">
                          <span class={['tm-status-dot', at.status === 'running' ? 'running' : 'idle']} />
                          <span class="tm-thread-title">{at.goal || at.title || at.id}</span>
                          <span class="tm-badge">{at.status}</span>
                        </div>
                        <div class="tm-thread-actions">
                          {at.status === 'running' && (
                            <button class="tm-btn sm" onClick={() => handleAdminAction('PauseAdminTask', at.id)}>
                              ⏸ 暂停
                            </button>
                          )}
                          {at.status === 'paused' && (
                            <button class="tm-btn sm primary" onClick={() => handleAdminAction('ResumeAdminTask', at.id)}>
                              ▶ 恢复
                            </button>
                          )}
                          {(at.status === 'failed' || at.status === 'cancelled') && (
                            <button class="tm-btn sm" onClick={() => handleAdminAction('RetryAdminTask', at.id)}>
                              🔄 重试
                            </button>
                          )}
                          {at.status === 'running' && (
                            <button class="tm-btn sm danger" onClick={() => handleAdminAction('CancelAdminTask', at.id)}>
                              ⏹ 取消
                            </button>
                          )}
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Background TaskHub History */}
            <div class="tm-section-card">
              <div class="tm-section-head">
                <div>
                  <h3 class="tm-section-title">📋 后台异步任务执行记录 ({bgTasks.value.length})</h3>
                  <p class="tm-section-desc">包含定时唤醒触发 (cron) 与手动派发 (manual) 的后台任务</p>
                </div>
              </div>

              {bgTasks.value.length === 0 ? (
                <div class="tm-empty">
                  <div class="tm-empty-title">暂无后台异步任务记录</div>
                  <div class="tm-empty-sub">在上方输入框派发任务或触发定时唤醒后，执行结果会在此汇总</div>
                </div>
              ) : (
                <div class="tm-list">
                  {bgTasks.value.map(t => {
                    // 库里写着 running 但本程序没在跑它：上次程序关掉时被打断的，别当成还在跑。
                    const interrupted = isInterrupted(t.status, liveTaskIds.value, t.id)
                    const tone = interrupted ? 'idle' : taskStatusTone(t.status)
                    return (
                      <div key={t.id} class={['tm-thread-card', tone]}>
                        <div class="tm-thread-top">
                          <div class="tm-thread-main">
                            <span class={['tm-status-dot', tone]} />
                            <span class="tm-spec-badge">{taskKindLabel(t.kind)}</span>
                            <span class="tm-thread-title" title={t.input}>{taskInputSummary(t) || '（没有内容）'}</span>
                            <span class={['tm-badge', tone]}>{interrupted ? '已中断' : taskStatusText(t.status)}</span>
                          </div>
                          <div class="tm-thread-actions">
                            {liveTaskIds.value.has(t.id) && (
                              <button class="tm-btn sm danger" onClick={() => cancelBgTask(t)}>
                                ⏹ 取消
                              </button>
                            )}
                            {t.session_id && (
                              <button class="tm-btn sm" onClick={() => jumpToSession(t.session_id!)}>
                                ↗ 查看会话
                              </button>
                            )}
                          </div>
                        </div>
                        {(t.result || t.error || interrupted) && (
                          <div class="tm-thread-detail">
                            {interrupted && <div class="tm-thread-hint">上次程序关闭时这个任务还没跑完，已中断。</div>}
                            {t.result && <div class="tm-bg-result">{String(t.result).slice(0, 360)}</div>}
                            {t.error && <div class="tm-thread-err">❌ {t.error}</div>}
                          </div>
                        )}
                      </div>
                    )
                  })}
                </div>
              )}
            </div>
          </div>
        )}
      </div>
    )
  },
})

export default TasksPanel

