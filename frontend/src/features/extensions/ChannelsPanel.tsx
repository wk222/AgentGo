import { defineComponent, ref, onMounted } from 'vue'
import { wailsCall } from '../../shared/wails'
import { panelBodyStyle, scrollStyle, EmptyState, PanelHeader, badge, rowDiv, statusColor } from '../../shared/ui/panelKit'

export const ChannelsPanel = defineComponent({
  name: 'ChannelsPanel',
  setup() {
    const items = ref<any[]>([])
    const loading = ref(true)
    onMounted(async () => {
      try { items.value = (await wailsCall('ListChannels')) || [] } catch {}
      loading.value = false
    })
    return () => (
      <div style={panelBodyStyle}>
        {PanelHeader('🌐', 'Channels')}
        <div style={scrollStyle}>
          {loading.value ? EmptyState('加载中…')
            : !items.value.length ? EmptyState('暂无 Channel。可通过环境变量启用 Slack/Telegram 等集成。')
            : items.value.map((ch: any) => rowDiv(<>
              <span style={{ width: '8px', height: '8px', borderRadius: '50%', background: statusColor(ch.state || ch.status), flexShrink: 0, display: 'block' }}></span>
              <div style={{ flex: 1, overflow: 'hidden' }}>
                <div style={{ fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{ch.channel || ch.name || ch.id}</div>
                {ch.kind ? <div style={{ fontSize: '11px', color: 'var(--text-dim)' }}>{ch.kind}</div> : null}
              </div>
              {badge(ch.state || ch.status || '未知', statusColor(ch.state || ch.status))}
            </>))}
        </div>
      </div>
    )
  },
})
