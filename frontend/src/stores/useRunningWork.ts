// 「运行中」：后端此刻在替用户干的活，和停着等用户的事（GetRunningWork）。
// 整个页面只有一份（模块单例），状态栏按钮、会话列表圆点都读它。
// 页面刷新后本地的运行状态全没了，这里是唯一还知道真相的地方。
import { computed, ref } from 'vue'
import { wailsCall } from '../shared/wails'
import type { RunningWork } from '../shared/ipc/types'
import { chipTone, emptyWork, remoteBadges, sameWork, workTotal } from './runningWorkModel'

const POLL_MS = 2000
/** 连着失败这么多次，就当作什么都不知道，免得一直显示过期的「运行中」。 */
const GIVE_UP_AFTER = 3

const work = ref<RunningWork>(emptyWork())
/** 最近一次拿到的服务端时间（unix 秒）；每次轮询都更新，列表内容没变也更新。 */
const serverNow = ref(0)

let users = 0
let timer: ReturnType<typeof setInterval> | null = null
let inflight: Promise<void> | null = null
let rerun = false
let failures = 0

async function fetchOnce(): Promise<void> {
  try {
    const next = await wailsCall('GetRunningWork')
    if (!next || !Array.isArray(next.items)) throw new Error('GetRunningWork 返回了意料之外的内容')
    failures = 0
    serverNow.value = next.at || Math.floor(Date.now() / 1000)
    // 内容没变就不换对象：会话列表、状态栏不会跟着每 2 秒重画一遍。
    if (!sameWork(work.value, next)) work.value = next
  } catch {
    failures++
    if (failures >= GIVE_UP_AFTER && work.value.items.length > 0) work.value = emptyWork()
  }
}

/** 立刻问一次。正在问的时候又被要求问，等这一次结束后再补一次：要求发出时的状态，不能被更早的那次顶替。 */
export function refreshRunningWork(): Promise<void> {
  if (inflight) {
    rerun = true
    return inflight
  }
  inflight = (async () => {
    try {
      do {
        rerun = false
        await fetchOnce()
      } while (rerun)
    } finally {
      inflight = null
    }
  })()
  return inflight
}

const tick = () => {
  if (typeof document !== 'undefined' && document.hidden) return // 没人看的时候不问
  void refreshRunningWork()
}
const onVisible = () => { if (!document.hidden) void refreshRunningWork() }

/** 开始轮询；谁调了 start，卸载时就要调一次 stop。多处同时用，只会有一个定时器。 */
function start() {
  if (users++ > 0) return
  void refreshRunningWork()
  timer = setInterval(tick, POLL_MS)
  document.addEventListener('visibilitychange', onVisible)
}

function stop() {
  if (--users > 0) return
  users = 0
  if (timer) clearInterval(timer)
  timer = null
  document.removeEventListener('visibilitychange', onVisible)
}

const badges = computed(() => remoteBadges(work.value))
const total = computed(() => workTotal(work.value))
const tone = computed(() => chipTone(work.value))

export function useRunningWork() {
  return { work, serverNow, badges, total, tone, refresh: refreshRunningWork, start, stop }
}
