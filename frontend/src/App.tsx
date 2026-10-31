import { defineAsyncComponent, defineComponent } from 'vue'
import UiHost from './shared/ui/UiHost'
import AppShell from './app/AppShell'

const WorkflowPanel = defineAsyncComponent(() =>
  import('./features/workflow/WorkflowPanel').then(m => m.WorkflowPanel),
)
const InnerAppHost = defineAsyncComponent(() => import('./features/extensions/InnerAppHost'))

// 入口只做一件事：按窗口参数决定渲染独立窗口还是完整壳。
// 独立窗口：?view=workflow&workflow_id=… / ?view=innerapp&app=…（兼容旧参数 ?workflow= / ?innerapp=）。
export default defineComponent({
  name: 'AgentGoApp',
  setup() {
    const params = new URLSearchParams(window.location.search)
    const view = params.get('view') || (params.has('workflow') ? 'workflow' : params.has('innerapp') ? 'innerapp' : '')

    if (view === 'workflow') {
      const workflowId = params.get('workflow_id') || params.get('workflow') || ''
      return () => (
        <div class="standalone-window standalone-window--workflow">
          <UiHost />
          <WorkflowPanel initialWorkflowId={workflowId} standalone={true} />
        </div>
      )
    }

    if (view === 'innerapp') {
      const appName = params.get('app') || params.get('innerapp') || ''
      return () => (
        <div class="standalone-window standalone-window--innerapp">
          <UiHost />
          <div class="standalone-window-head">
            <span>{appName || 'Inner App'}</span>
          </div>
          <div class="standalone-window-body">
            <InnerAppHost appName={appName} />
          </div>
        </div>
      )
    }

    return () => <AppShell />
  },
})
