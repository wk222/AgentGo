// 按轮回退的界面状态与动作：哪些回退点还在、回退前的预览与确认、回退后的撤销条。
// 说什么、什么时候出按钮是纯逻辑，放在 stores/turnCheckpointModel.ts，单测在那边。
import { computed, reactive, ref, watch } from 'vue'
import type { Message } from '../../stores/useChat'
import { wailsCall } from '../../shared/wails'
import { notify, confirmDialog } from '../../shared/ui/notify'
import type { ChatContext } from './chatContext'
import {
  canRollback, checkpointOf, describeRollback, liveCheckpointIds,
  rollbackError, rollbackNotice, rollbackSummary, undoSettled,
} from '../../stores/turnCheckpointModel'

/** 刚做完一次回退后的"撤销"入口：id 是回退前自动存的那份现状。 */
export interface UndoBar {
  id: string
  text: string
}

// 撤销条的状态放在模块里、按会话记，不放在组件里：回退完要 reload 对话，reload 期间 ChatPanel
// 会先卸载对话区再挂回（加载占位），组件里的本地状态会被一起清掉，撤销条就没了。
// 回退点失效（重启、换工作区）时撤销会得到"找不到"，那时 undoSettled 会把它收起来。
const undoBars = reactive<Record<string, UndoBar>>({})

export function useTurnRollback(chat: ChatContext) {
  const live = ref<Record<string, true>>({})
  const busy = ref(false)
  const undoBar = computed(() => undoBars[chat.sessionId] ?? null)
  let refreshSeq = 0

  // 后端只列当前工作区里还能回退的轮次（重启后、被新回退点挤掉、换过工作区的都不在）。
  const refresh = async () => {
    const sid = chat.sessionId
    const seq = ++refreshSeq
    if (!sid) { live.value = {}; return }
    try {
      const r = await wailsCall('ListTurnCheckpoints', sid)
      if (seq === refreshSeq) live.value = liveCheckpointIds(r)
    } catch {
      if (seq === refreshSeq) live.value = {}
    }
  }

  // 运行中不查：每来一条消息都查太勤，而且那时本来也不给回退。一轮结束、换会话、消息数变了再查。
  watch(
    [() => chat.sessionId, () => chat.sending, () => chat.messages.length],
    () => { if (!chat.sending) void refresh() },
    { immediate: true },
  )

  const can = (msg: Message) => canRollback(msg, live.value, chat.sending)

  const rollbackTo = async (msg: Message) => {
    const sid = chat.sessionId
    const id = checkpointOf(msg)
    if (!sid || !id || busy.value) return
    busy.value = true
    try {
      const pv = await wailsCall('PreviewTurnRollback', sid, id)
      if (!pv?.success || pv.busy) {
        const e = rollbackError(pv)
        notify[e.tone](e.text)
        return
      }
      const d = describeRollback(pv.preview)
      if (d.empty) {
        notify.info(d.text.replace(/\s*\n\s*/g, ' ').trim())
        return
      }
      const ok = await confirmDialog(d.text, { title: '回到这一轮之前', okText: '确认回退', danger: true })
      if (!ok) return
      const res = await wailsCall('RollbackToTurn', sid, id)
      const n = rollbackNotice(res)
      notify[n.tone](n.text)
      const r = res?.result
      if (r && r.restored + r.recreated + r.deleted > 0 && res.undo_checkpoint_id) {
        undoBars[sid] = { id: res.undo_checkpoint_id, text: `已回到那一轮之前（${rollbackSummary(r)}）` }
      }
      chat.reload()
    } catch (e: any) {
      notify.error(`回退失败：${e?.message || e}`)
    } finally {
      busy.value = false
      void refresh()
    }
  }

  const undo = async () => {
    const bar = undoBar.value
    const sid = chat.sessionId
    if (!bar || !sid || busy.value || chat.sending) return
    busy.value = true
    try {
      const res = await wailsCall('RollbackToTurn', sid, bar.id)
      const n = rollbackNotice(res, true)
      notify[n.tone](n.text)
      if (undoSettled(res)) delete undoBars[sid]
      chat.reload()
    } catch (e: any) {
      notify.error(`撤销失败：${e?.message || e}`)
    } finally {
      busy.value = false
      void refresh()
    }
  }

  return { can, busy, undoBar, rollbackTo, undo, dismissUndo: () => { delete undoBars[chat.sessionId] } }
}
