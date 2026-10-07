import { defineComponent } from 'vue'
import { Activity, Cpu, HardDrive, Terminal } from 'lucide-vue-next'

export default defineComponent({
  name: 'AppStatusBar',
  props: {
    model: { type: String, default: 'gpt-4o' },
    modeProfile: { type: String, default: 'assistant' },
    modeCanvas: { type: String, default: 'balanced' },
    sending: { type: Boolean, default: false },
    sessionId: { type: String, default: '' },
    statusLine: { type: String, default: '' },
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

          <span class="statusbar-item" title="智能体运行模式与深度">
            <Activity size={12} class="statusbar-icon" />
            <span>{props.modeProfile} · {props.modeCanvas}</span>
          </span>
        </div>

        <div class="statusbar-center">
          {props.sessionId && (
            <span class="statusbar-item" title={`当前会话: ${props.sessionId}`}>
              <Terminal size={12} class="statusbar-icon" />
              <span>会话: {props.sessionId.slice(0, 12)}…</span>
            </span>
          )}
        </div>

        <div class="statusbar-right">
          <span class="statusbar-item" title="Eino v0.10.0-alpha.32 ADK 运行时就绪">
            <HardDrive size={12} class="statusbar-icon" />
            <span>Eino v0.10 ADK</span>
          </span>

          <span class="statusbar-divider"></span>

          <span class="statusbar-item hints" title="快捷键帮助">
            <span>Ctrl+B 侧栏 · Enter 发送</span>
          </span>
        </div>
      </footer>
    )
  },
})
