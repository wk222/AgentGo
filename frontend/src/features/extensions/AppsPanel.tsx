import { defineComponent, ref, onMounted, watch, type PropType } from 'vue'
import { wailsCall } from '../../shared/wails'
import InnerAppHost from './InnerAppHost'
import { stageState } from '../../stores/agentBridge'
import { panelBodyStyle, scrollStyle, EmptyState, PanelHeader, badge, miniButtonStyle, rowDiv } from '../../shared/ui/panelKit'

export const AppsPanel = defineComponent({
  name: 'AppsPanel',
  props: {
    onSendToAgent: { type: Function as PropType<(text: string) => void>, default: undefined },
  },
  setup(props) {
    const items = ref<any[]>([])
    const loading = ref(true)
    const status = ref('')
    const showBuilder = ref(false)
    const building = ref(false)
    const viewerApp = ref<any | null>(null)
    const buildForm = ref({
      name: '',
      displayName: '',
      description: '',
      mode: 'static',
      workflowID: '',
      systemPrompt: '',
      overwrite: false,
    })
    const load = async () => {
      loading.value = true
      try { items.value = (await wailsCall('ListInnerApps', 50)) || [] } catch {}
      loading.value = false
    }
    const appName = (app: any) => String(app.name || app.id || '').replace(/^app:/, '')
    const hasUI = (app: any) => !!(app.has_ui || app.kind === 'ui' || app.bundle_path || app.url)
    onMounted(() => { void load() })
    watch(
      () => [stageState.activeAppName, stageState.activeAppVersion] as const,
      async ([name]) => {
        if (!name) return
        await load()
        const matched = items.value.find(a => appName(a) === name)
        viewerApp.value = matched || { name, has_ui: true }
        status.value = `已联动打开 Inner App 画布：${name}`
      },
      { immediate: true },
    )
    const askAgent = (app: any) => {
      props.onSendToAgent?.(`请评估并在合适时调用内置应用 ${appName(app)}。\n\n应用描述：${app.description || ''}\n请说明它适合处理什么输入、需要哪些权限或上下文。`)
    }
    const openUI = async (app: any) => {
      status.value = ''
      const name = appName(app)
      try {
        const r = await wailsCall('OpenInnerAppWindow', name)
        if (r?.success) {
          viewerApp.value = null
          status.value = `已在独立窗口打开 ${name}`
          return
        }
        status.value = r?.error ? `独立窗口不可用，已切到内嵌预览：${r.error}` : ''
      } catch {}
      viewerApp.value = app
    }
    const pingApp = async (app: any) => {
      status.value = ''
      try {
        const r = await wailsCall('InvokeInnerApp', appName(app), 'health check from UI', '')
        status.value = r?.success ? (r.content || '调用成功') : (r?.error || '调用失败')
      } catch (e: any) { status.value = e?.message || '调用失败' }
    }
    const buildApp = async () => {
      const form = buildForm.value
      if (!form.name.trim()) {
        status.value = '请先填写应用名'
        return
      }
      building.value = true
      status.value = ''
      try {
        const r = await wailsCall(
          'BuildInnerAppIteratively',
          form.name.trim(),
          form.displayName.trim(),
          form.description.trim(),
          form.mode,
          form.workflowID.trim(),
          form.systemPrompt.trim(),
          3,
          form.overwrite,
        )
        status.value = r?.success
          ? `已生成 ${r.app_name || form.name}：${r.message || '完成'}`
          : (r?.error || '生成失败')
        await load()
      } catch (e: any) {
        status.value = e?.message || '生成失败'
      } finally {
        building.value = false
      }
    }
    return () => (
      <div style={panelBodyStyle}>
        {PanelHeader('📦', '内置应用')}
        <div style={scrollStyle}>
          <div style={{ display: 'flex', gap: '6px', marginBottom: '12px', flexWrap: 'wrap' }}>
            <button style={miniButtonStyle(true)} onClick={() => showBuilder.value = !showBuilder.value}>
              {showBuilder.value ? '收起生成器' : '生成 Inner App'}
            </button>
            <button style={miniButtonStyle()} onClick={load}>刷新</button>
          </div>
          {showBuilder.value && (
            <div style={{ border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', padding: '12px', marginBottom: '12px', background: 'var(--surface-2)' }}>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '8px', marginBottom: '8px' }}>
                <input style={{ padding: '7px 9px', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', background: 'var(--input-bg)', color: 'var(--text)' }} placeholder="应用名 demo_app" value={buildForm.value.name} onInput={(e: Event) => buildForm.value = { ...buildForm.value, name: (e.target as HTMLInputElement).value }} />
                <input style={{ padding: '7px 9px', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', background: 'var(--input-bg)', color: 'var(--text)' }} placeholder="显示名" value={buildForm.value.displayName} onInput={(e: Event) => buildForm.value = { ...buildForm.value, displayName: (e.target as HTMLInputElement).value }} />
              </div>
              <textarea style={{ width: '100%', minHeight: '70px', padding: '8px 9px', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', background: 'var(--input-bg)', color: 'var(--text)', resize: 'vertical', marginBottom: '8px' }} placeholder="描述这个 app 要解决什么问题，AI 会基于它生成 UI bundle。" value={buildForm.value.description} onInput={(e: Event) => buildForm.value = { ...buildForm.value, description: (e.target as HTMLTextAreaElement).value }} />
              <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap', alignItems: 'center' }}>
                <select style={{ padding: '6px 9px', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', background: 'var(--input-bg)', color: 'var(--text)' }} value={buildForm.value.mode} onChange={(e: Event) => buildForm.value = { ...buildForm.value, mode: (e.target as HTMLSelectElement).value }}>
                  <option value="static">静态 UI</option>
                  <option value="chat">聊天 UI</option>
                  <option value="workflow">工作流 UI</option>
                </select>
                {buildForm.value.mode === 'workflow' && (
                  <input style={{ padding: '6px 9px', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', background: 'var(--input-bg)', color: 'var(--text)' }} placeholder="workflow_id" value={buildForm.value.workflowID} onInput={(e: Event) => buildForm.value = { ...buildForm.value, workflowID: (e.target as HTMLInputElement).value }} />
                )}
                <label style={{ display: 'flex', alignItems: 'center', gap: '5px', color: 'var(--text-dim)', fontSize: '12px' }}>
                  <input type="checkbox" checked={buildForm.value.overwrite} onChange={(e: Event) => buildForm.value = { ...buildForm.value, overwrite: (e.target as HTMLInputElement).checked }} />
                  覆盖同名
                </label>
                <button style={miniButtonStyle(true)} disabled={building.value} onClick={buildApp}>
                  {building.value ? '生成中…' : '开始生成'}
                </button>
              </div>
            </div>
          )}
          {status.value && <div style={{ color: 'var(--text-dim)', fontSize: '12px', marginBottom: '12px', lineHeight: 1.5 }}>{status.value}</div>}
          {viewerApp.value && (
            <div class="inner-app-viewer">
              <div class="inner-app-viewer-head">
                <span>{appName(viewerApp.value)}</span>
                <button style={miniButtonStyle()} onClick={() => { viewerApp.value = null }}>关闭</button>
              </div>
              <InnerAppHost
                appName={appName(viewerApp.value)}
                refreshToken={stageState.activeAppVersion}
                onSendToAgent={props.onSendToAgent}
              />
            </div>
          )}
          {loading.value ? EmptyState('加载中…')
            : !items.value.length ? EmptyState('暂无内置应用。通过对话激活应用插件。')
            : items.value.map((app: any) => rowDiv(<>
              <span style={{ fontSize: '20px', flexShrink: 0 }}>{app.icon || '📦'}</span>
              <div style={{ flex: 1, overflow: 'hidden' }}>
                <div style={{ fontWeight: 500 }}>{app.name || app.id}</div>
                {app.description ? <div style={{ fontSize: '11px', color: 'var(--text-dim)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{app.description}</div> : null}
              </div>
              {badge(hasUI(app) ? 'UI' : 'Callable App', hasUI(app) ? '#3b9eff' : undefined)}
              {app.enabled !== undefined ? badge(app.enabled ? '启用' : '未启用', app.enabled ? '#5cb85c' : undefined) : null}
              {hasUI(app) ? <button style={miniButtonStyle(true)} onClick={() => openUI(app)}>打开 UI</button> : null}
              <button style={miniButtonStyle(true)} disabled={!props.onSendToAgent} onClick={() => askAgent(app)}>调用建议</button>
              <button style={miniButtonStyle()} onClick={() => pingApp(app)}>健康检查</button>
            </>))}
        </div>
      </div>
    )
  },
})
