import { defineComponent, ref, computed, watch, PropType } from 'vue'
import { UserRound, ChevronDown, ChevronRight, X } from 'lucide-vue-next'
import { useChatContext } from './chatContext'
import { wailsCall } from '../../shared/wails'
import { notify } from '../../shared/ui/notify'
import type { ExpertInfo } from '../../shared/ipc/types'
import {
  MISSING_EXPERT_HINT, expertChip, expertNotice, expertState, expertTooltip, folderHint, groupExperts, pickStarters, sourceLabel,
  type ExpertState,
} from '../../stores/expertModel'
import './expert.css'

const NO_EXPERT = expertState(null)

/**
 * 输入框上方的「专家」条：这条对话以什么角色工作。
 * 折叠时只显示当前专家（没有就写「未选择」）；展开后按分类列出专家，点一下就绑定到当前对话，
 * 从下一条消息起生效。专家只改提示词，不改审批和沙箱。已经选了专家且对话还空着时，
 * 把它的示例提示列成小按钮。
 */
export default defineComponent({
  name: 'ExpertBar',
  props: {
    /** 点示例提示：把它填进输入框，由用户决定发不发。 */
    onPickStarter: { type: Function as PropType<(prompt: string) => void>, required: true },
  },
  setup(props) {
    const ctx = useChatContext()
    const open = ref(false)
    const experts = ref<ExpertInfo[]>([])
    const warnings = ref<string[]>([])
    const dirs = ref({ user: '', workspace: '' })
    const listState = ref<'idle' | 'loading' | 'error'>('idle')
    const listError = ref('')
    const state = ref<ExpertState>(NO_EXPERT)
    const busy = ref(false)
    let currentSeq = 0
    let listSeq = 0

    const hasUserMessage = computed(() => ctx.messages.some(m => m.role === 'user'))

    // 换对话就重新读它的专家；较慢的旧回答不能盖掉新对话的结果。
    const refreshCurrent = async (sid: string) => {
      const mine = ++currentSeq
      if (!sid) { state.value = NO_EXPERT; return }
      try {
        const r = await wailsCall('GetSessionExpert', sid)
        if (mine === currentSeq) state.value = r?.success ? expertState(r) : NO_EXPERT
      } catch {
        if (mine === currentSeq) state.value = NO_EXPERT
      }
    }
    watch(() => ctx.sessionId, sid => { void refreshCurrent(sid) }, { immediate: true })

    const loadList = async () => {
      const mine = ++listSeq
      listState.value = 'loading'
      try {
        const r = await wailsCall('ListExperts')
        if (mine !== listSeq) return
        if (!r?.success) throw new Error(String(r?.error || '读取专家列表失败'))
        experts.value = r.experts || []
        warnings.value = r.warnings || []
        dirs.value = { user: r.user_dir || '', workspace: r.workspace_dir || '' }
        listState.value = 'idle'
      } catch (e: any) {
        if (mine !== listSeq) return
        listError.value = String(e?.message || e)
        listState.value = 'error'
      }
    }

    const toggle = () => {
      open.value = !open.value
      if (open.value) void loadList() // 每次展开都重读：文件改了、功能开关变了，这里立刻跟上
    }

    // key 为空 = 取消专家。还没有对话时先建一条，再绑定。
    const apply = async (key: string) => {
      if (busy.value) return
      busy.value = true
      try {
        const hadMessages = hasUserMessage.value
        let sid = ctx.sessionId
        if (!sid) {
          await ctx.newChat()
          sid = ctx.sessionId
        }
        if (!sid) { notify.error('没能创建对话，专家没有设置'); return }
        let r: any
        try { r = await wailsCall('SetSessionExpert', sid, key) } catch (e: any) { r = { success: false, error: String(e?.message || e) } }
        const note = expertNotice(r, hadMessages)
        notify[note.tone](note.text)
        if (r?.success) {
          currentSeq++ // 作废还在路上的旧读取
          state.value = expertState(r)
        }
      } finally { busy.value = false }
    }

    return () => {
      const st = state.value
      const groups = groupExperts(experts.value)
      const starters = st.kind === 'active' && !hasUserMessage.value ? pickStarters(st.expert) : []
      const folders = folderHint(dirs.value.user, dirs.value.workspace)
      return (
        <div class="expert-bar">
          <button type="button" class="expert-bar-head" aria-expanded={open.value} onClick={toggle}>
            <UserRound size={12} class="expert-bar-icon" />
            <span>专家</span>
            {st.kind === 'none'
              ? <span class="expert-bar-hint">未选择</span>
              : (
                <span
                  class={['expert-bar-current', st.kind === 'missing' && 'is-missing']}
                  title={st.kind === 'missing' ? MISSING_EXPERT_HINT : st.expert?.summary}
                >
                  {expertChip(st)}
                </span>
              )}
            <span class="expert-bar-meta">{open.value ? <ChevronDown size={13} /> : <ChevronRight size={13} />}</span>
          </button>

          {st.kind === 'missing' && <div class="expert-bar-note is-warn">{MISSING_EXPERT_HINT}</div>}

          {starters.length > 0 && (
            <div class="expert-starters">
              {starters.map((text, idx) => (
                <button key={idx} type="button" class="expert-starter-btn" title={text} onClick={() => props.onPickStarter(text)}>
                  {text}
                </button>
              ))}
            </div>
          )}

          {open.value && (
            <div class="expert-bar-body">
              {st.kind !== 'none' && (
                <div class="expert-bar-active">
                  <span class="expert-bar-active-text">{st.kind === 'active' ? st.expert?.summary : ''}</span>
                  <button type="button" class="expert-clear-btn" disabled={busy.value} onClick={() => { void apply('') }}>
                    <X size={11} />
                    <span>取消专家</span>
                  </button>
                </div>
              )}

              {listState.value === 'loading' && experts.value.length === 0 && <div class="expert-bar-note">正在读取专家…</div>}
              {listState.value === 'error' && (
                <div class="expert-bar-note is-error">
                  <span>{listError.value}</span>
                  <button type="button" class="expert-link-btn" onClick={() => { void loadList() }}>重试</button>
                </div>
              )}

              {groups.map(g => (
                <div class="expert-group" key={g.category}>
                  <div class="expert-group-title">{g.label}</div>
                  <div class="expert-cards">
                    {g.experts.map(e => (
                      <button
                        key={e.id}
                        type="button"
                        class={['expert-card', e.id === st.id && 'active']}
                        disabled={busy.value}
                        title={expertTooltip(e)}
                        onClick={() => { void apply(e.id) }}
                      >
                        <span class="expert-card-name">
                          {e.name}
                          {sourceLabel(e) && <span class="expert-card-tag">{sourceLabel(e)}</span>}
                        </span>
                        <span class="expert-card-sum">{e.summary}</span>
                      </button>
                    ))}
                  </div>
                </div>
              ))}

              {warnings.value.length > 0 && (
                <div class="expert-bar-note is-warn" title={warnings.value.join('\n')}>
                  有 {warnings.value.length} 个专家文件没能读取（鼠标悬停看原因）
                </div>
              )}
              {folders && (
                <div class="expert-bar-foot">
                  <div>想要自己的专家：在下面的目录里放 .md 文件（开头写 name、summary，正文写规则），改完下一条消息就生效。</div>
                  <div class="expert-bar-paths">{folders}</div>
                </div>
              )}
            </div>
          )}
        </div>
      )
    }
  },
})
