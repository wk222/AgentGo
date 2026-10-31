import { computed, defineComponent, nextTick, onBeforeUnmount, reactive, ref, watch, type PropType } from 'vue'
import type { RunningWorkItem } from '../shared/ipc/types'
import { useRunningWork } from '../stores/useRunningWork'
import { canJump, chipText, isWaiting, itemDetail, itemTitle, jumpLabel, panelShift, stopLabel } from '../stores/runningWorkModel'
import './running-work.css'

/** 状态栏上的「运行中」：此刻在替你干的活，和停着等你的事。
 *  数据来自 useRunningWork（每 2 秒问一次后端），点一行跳到对应对话，点停止就停。 */
export default defineComponent({
  name: 'RunningWorkMenu',
  props: {
    currentSessionId: { type: String, default: '' },
    onJump: { type: Function as PropType<(item: RunningWorkItem) => void>, required: true },
    onStop: { type: Function as PropType<(item: RunningWorkItem) => Promise<void> | void>, required: true },
  },
  setup(props) {
    const { work, serverNow, total, tone, refresh } = useRunningWork()
    const open = ref(false)
    const stopping = reactive<Record<string, boolean>>({})

    // 打开时立刻刷新一次；列表空了而面板还开着，就留着，别让面板凭空消失。
    watch(open, isOpen => { if (isOpen) void refresh() })
    const shown = computed(() => total.value > 0 || open.value)

    // 弹层贴着按钮左边弹出，窗口窄时右边会伸出窗外：打开时、窗口大小变化时，按实际位置挪回来。
    const panel = ref<HTMLElement | null>(null)
    const fit = () => {
      const el = panel.value
      if (!el) return
      el.style.transform = ''
      const r = el.getBoundingClientRect()
      const dx = panelShift(r.left, r.right, window.innerWidth)
      if (dx) el.style.transform = `translateX(${dx}px)`
    }

    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') open.value = false }
    watch(open, isOpen => {
      if (isOpen) {
        window.addEventListener('keydown', onKey)
        window.addEventListener('resize', fit)
        void nextTick(fit)
      } else {
        window.removeEventListener('keydown', onKey)
        window.removeEventListener('resize', fit)
      }
    })
    onBeforeUnmount(() => {
      window.removeEventListener('keydown', onKey)
      window.removeEventListener('resize', fit)
    })

    const jump = (it: RunningWorkItem) => {
      if (!canJump(it)) return
      open.value = false
      props.onJump(it)
    }
    const stop = async (it: RunningWorkItem) => {
      if (stopping[it.id]) return
      stopping[it.id] = true
      try { await props.onStop(it) } finally {
        delete stopping[it.id]
        void refresh()
      }
    }

    const renderRow = (it: RunningWorkItem) => {
      const jumpable = canJump(it)
      const current = !!it.session_id && it.session_id === props.currentSessionId
      return (
        <li key={it.id} class={['rw-row', `is-${it.state}`, current && 'is-current']} data-kind={it.kind}>
          <span class="rw-dot" aria-hidden="true" />
          <button
            type="button"
            class="rw-main"
            disabled={!jumpable}
            title={jumpable ? `${jumpLabel(it)}：打开这个对话` : it.session_id ? '这个对话已不在列表里，打不开' : undefined}
            onClick={() => jump(it)}
          >
            <span class="rw-title">{itemTitle(it)}{current && <em class="rw-tag">当前</em>}</span>
            <span class="rw-detail">{itemDetail(it, serverNow.value)}</span>
          </button>
          {jumpable && isWaiting(it) && (
            <button type="button" class="rw-act rw-act-go" onClick={() => jump(it)}>{jumpLabel(it)}</button>
          )}
          {it.stoppable && (
            <button type="button" class="rw-act rw-act-stop" disabled={!!stopping[it.id]} onClick={() => void stop(it)}>
              {stopping[it.id] ? '正在停…' : stopLabel(it)}
            </button>
          )}
        </li>
      )
    }

    return () => {
      if (!shown.value) return null
      const w = work.value
      return (
        <span class="rw">
          <button
            type="button"
            class={['statusbar-item', 'rw-trigger', `is-${tone.value}`]}
            aria-haspopup="dialog"
            aria-expanded={open.value}
            title="查看正在运行、和停着等你处理的工作"
            onClick={() => { open.value = !open.value }}
          >
            <span class="rw-trigger-dot" aria-hidden="true" />
            <span>{chipText(w)}</span>
          </button>
          {open.value && <div class="rw-backdrop" onClick={() => { open.value = false }} />}
          {open.value && (
            <div ref={panel} class="rw-panel" role="dialog" aria-label="运行中的工作">
              <header class="rw-head">
                <strong>运行中的工作</strong>
                <span class="rw-count">{w.items.length ? `${w.items.length} 项` : ''}</span>
              </header>
              {w.items.length === 0
                ? <p class="rw-empty">现在没有在运行的工作，也没有等你处理的事。</p>
                : <ul class="rw-list">{w.items.map(renderRow)}</ul>}
              <footer class="rw-foot">只显示这个程序里的工作；在别的窗口或进程里运行的看不到。</footer>
            </div>
          )}
        </span>
      )
    }
  },
})
