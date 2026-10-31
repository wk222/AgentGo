import { defineComponent } from 'vue'
import { WORKBENCH_VIEWS } from './viewRegistry'
import { useLayout } from '../stores/useLayout'

// Mounted inside the workbench header, so hiding the pane hides its switcher.
export default defineComponent({
  name: 'WorkbenchTabs',
  setup() {
    const layout = useLayout()
    return () => <nav class="workbench-type-tabs" aria-label="工作台类型">
      {WORKBENCH_VIEWS.map(view => {
        const Icon = view.icon
        const selected = layout.state.workbenchType === view.id
        return <button type="button" key={view.id}
          class={['workbench-type-tab', selected && 'active']}
          aria-label={'切换到' + view.label + '工作台'} aria-pressed={selected}
          title={view.hint} onClick={() => layout.openWorkbench(view.id as any)}>
          <Icon size={13}/><span>{view.label}</span>
        </button>
      })}
    </nav>
  },
})
