import { defineComponent } from 'vue'
import { PanelRight } from 'lucide-vue-next'
import { VIEWS } from './viewRegistry'
import { useLayout } from '../stores/useLayout'

// 48px 图标栏：只放"主区视图"。设置在标题栏；Agent 是区域不是视图。
export default defineComponent({
  name: 'ActivityBar',
  props: {
    /** 等待用户处理的审批/提问数，显示在"Agent"入口之外的位置会漏看，所以放在上下文按钮旁。 */
    pending: { type: Number, default: 0 },
  },
  setup(props) {
    const layout = useLayout()
    return () => {
      const s = layout.state
      return (
        <nav class="activitybar" aria-label="主导航">
          <div class="activitybar-group">
            {VIEWS.map(v => {
              const Icon = v.icon
              const active = s.view === v.id && s.agent !== 'maximized'
              return (
                <button
                  key={v.id}
                  class={['ab-btn', active && 'active']}
                  title={`${v.label} — ${v.hint}`}
                  aria-label={v.label}
                  aria-current={active ? 'page' : undefined}
                  onClick={() => layout.setView(v.id)}
                >
                  <Icon size={18} stroke-width={1.8} />
                  <span class="ab-label">{v.label}</span>
                </button>
              )
            })}
          </div>
          <div class="activitybar-group activitybar-bottom">
            <button
              class={['ab-btn', s.contextOpen && 'active']}
              title="上下文抽屉：工作区文件与要带给 Agent 的上下文"
              aria-label="上下文"
              onClick={() => layout.toggleContext()}
            >
              <PanelRight size={18} stroke-width={1.8} />
              <span class="ab-label">上下文</span>
              {props.pending > 0 && <span class="ab-badge">{props.pending}</span>}
            </button>
          </div>
        </nav>
      )
    }
  },
})
