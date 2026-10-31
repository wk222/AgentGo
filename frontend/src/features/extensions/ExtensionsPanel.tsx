import { defineComponent, ref, watch } from 'vue'
import { AppsPanel } from './AppsPanel'
import { CapabilityPanel } from './CapabilityPanel'
import { SkillsPanel } from './SkillsPanel'
import { ChannelsPanel } from './ChannelsPanel'
import { sendToAgent, stageState } from '../../stores/agentBridge'

// "扩展"= 以前分散在导航里的 内置应用 / 能力中心 / Skills / Channels，作为同一视图的页签。
const TABS = [
  { id: 'apps', label: '内置应用' },
  { id: 'capabilities', label: '能力' },
  { id: 'skills', label: 'Skills' },
  { id: 'channels', label: '渠道' },
] as const
type TabId = typeof TABS[number]['id']

const TAB_KEY = 'agentgo.extensions.tab'

export default defineComponent({
  name: 'ExtensionsPanel',
  setup() {
    const saved = (() => { try { return localStorage.getItem(TAB_KEY) } catch { return null } })()
    const tab = ref<TabId>(TABS.some(t => t.id === saved) ? (saved as TabId) : 'apps')
    const select = (id: TabId) => {
      tab.value = id
      try { localStorage.setItem(TAB_KEY, id) } catch { /* 偏好可丢 */ }
    }
    watch(() => stageState.activeAppName, name => {
      if (name) select('apps')
    }, { immediate: true })
    return () => (
      <div class="ext-panel">
        <div class="ext-tabs" role="tablist">
          {TABS.map(t => (
            <button
              key={t.id}
              role="tab"
              aria-selected={tab.value === t.id}
              class={['ext-tab', tab.value === t.id && 'active']}
              onClick={() => select(t.id)}
            >{t.label}</button>
          ))}
        </div>
        <div class="ext-body">
          {tab.value === 'apps' && <AppsPanel onSendToAgent={sendToAgent} />}
          {tab.value === 'capabilities' && <CapabilityPanel onSendToAgent={sendToAgent} />}
          {tab.value === 'skills' && <SkillsPanel onSendToAgent={sendToAgent} />}
          {tab.value === 'channels' && <ChannelsPanel />}
        </div>
      </div>
    )
  },
})
