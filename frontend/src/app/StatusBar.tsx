import { defineComponent, type PropType } from 'vue'
import { Activity, Cpu } from 'lucide-vue-next'
import { harnessLabel } from '../shared/harness'
import type { RunningWorkItem } from '../shared/ipc/types'
import ContextMeter from './ContextMeter'
import RunningWorkMenu from './RunningWork'

const formatHarnessLabel = harnessLabel

export default defineComponent({
  name: 'AppStatusBar',
  props: {
    model: { type: String, default: 'gpt-4o' },
    modeProfile: { type: String, default: 'assistant' },
    modeCanvas: { type: String, default: 'balanced' },
    sending: { type: Boolean, default: false },
    sessionId: { type: String, default: '' },
    statusLine: { type: String, default: '' },
    /** 「运行中」列表里点某一行：跳到它的对话。 */
    onJumpWork: { type: Function as PropType<(item: RunningWorkItem) => void>, default: () => {} },
    /** 「运行中」列表里点停止/取消。 */
    onStopWork: { type: Function as PropType<(item: RunningWorkItem) => Promise<void> | void>, default: () => {} },
  },
  setup(props) {
    return () => (
      <footer class="app-statusbar">
        <div class="statusbar-left">
          <span class={['statusbar-item', props.sending ? 'status-busy' : 'status-ready']}>
            <span class="status-indicator-dot"></span>
            <span>{props.sending ? (props.statusLine || 'Agent 运行中…') : '就绪'}</span>
          </span>

          <span class="statusbar-divider"></span>

          <span class="statusbar-item" title="当前主大语言模型">
            <Cpu size={12} class="statusbar-icon" />
            <span class="statusbar-badge">{props.model || 'GPT-4o'}</span>
          </span>

          <span class="statusbar-item" title={`Agent Harness: ${props.modeProfile} · ${props.modeCanvas}`}>
            <Activity size={12} class="statusbar-icon" />
            <span>{formatHarnessLabel(props.modeProfile, props.modeCanvas)}</span>
          </span>

          <ContextMeter sessionId={props.sessionId} sending={props.sending} />
          <RunningWorkMenu currentSessionId={props.sessionId} onJump={props.onJumpWork} onStop={props.onStopWork} />
        </div>

        <div class="statusbar-right">
          <span class="statusbar-item hints" title="快捷键帮助">
            <span>Ctrl+B 侧栏 · Enter 发送</span>
          </span>
        </div>
      </footer>
    )
  },
})
