import { defineComponent } from 'vue'
import { Settings, AppWindow, X, MessageSquare } from 'lucide-vue-next'
import { VIEW_BY_ID } from './viewRegistry'
import { generatedPageName } from '../stores/generatedPages'
import { useLayout } from '../stores/useLayout'

// Page navigation is always outside the workbench. No pane controls here.
export default defineComponent({
  name: 'SceneTabBar',
  setup() {
    const layout = useLayout()
    return () => (
      <nav class="page-tabbar" aria-label="主页面">
        <div class="page-tabs" role="tablist" aria-label="已打开页面">
          {layout.sceneTabs.openTabs.map(id => {
            const generated = generatedPageName(id)
            const meta = id === 'settings' ? { label: '设置', icon: Settings }
              : VIEW_BY_ID.get(id) || (generated ? { label: generated, icon: AppWindow } : null)
            if (!meta) return null
            const Icon = meta.icon
            const selected = layout.state.view === id
            return <div key={id} class={['page-tab', selected && 'active']}>
              <button type="button" role="tab" aria-selected={selected} aria-controls={'page-' + id}
                onClick={() => layout.selectScene(id)}
                onKeydown={(e: KeyboardEvent) => {
                  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) return
                  e.preventDefault()
                  const buttons = Array.from((e.currentTarget as HTMLElement).closest('[role=tablist]')!.querySelectorAll<HTMLButtonElement>('[role=tab]'))
                  const index = buttons.indexOf(e.currentTarget as HTMLButtonElement)
                  const next = e.key === 'Home' ? 0 : e.key === 'End' ? buttons.length - 1 : (index + (e.key === 'ArrowRight' ? 1 : -1) + buttons.length) % buttons.length
                  buttons[next]?.focus(); buttons[next]?.click()
                }}>
                <Icon size={14}/><span>{meta.label}</span>
              </button>
              {id !== 'chat' && <button type="button" class="page-tab-close" aria-label={'关闭' + meta.label}
                onClick={() => layout.closeSceneTab(id)}><X size={12}/></button>}
            </div>
          })}
        </div>

      </nav>
    )
  },
})
