import { defineComponent, ref, computed } from 'vue'
import { wailsCall } from '../wails'
import type { ConfigEntry } from '../ipc/types'

const GROUP_LABELS: Record<string, string> = {
  model: '模型',
  agent: 'Agent',
  memory: '记忆',
  matrix: 'Matrix',
  gateway: '网关',
  plugins: '插件',
  sandbox: '沙箱',
  workflow: '工作流',
  debug: '调试',
}

/**
 * 只读的「高级配置」：列出所有 AGENTGO_* 环境开关的当前生效值、默认值与说明。
 * 修改方式仍是设置环境变量后重启，这里只负责让它们可见、可核对。
 */
export default defineComponent({
  name: 'AdvancedConfigCard',
  setup() {
    const open = ref(false)
    const loading = ref(false)
    const error = ref('')
    const entries = ref<ConfigEntry[]>([])
    const onlyOverridden = ref(false)

    const load = async () => {
      loading.value = true
      error.value = ''
      try {
        entries.value = (await wailsCall('GetConfigSnapshot')) || []
      } catch (e) {
        error.value = e instanceof Error ? e.message : String(e)
      } finally {
        loading.value = false
      }
    }

    const toggle = () => {
      open.value = !open.value
      if (open.value && entries.value.length === 0) void load()
    }

    const groups = computed(() => {
      const byGroup = new Map<string, ConfigEntry[]>()
      for (const e of entries.value) {
        if (onlyOverridden.value && !e.set) continue
        const list = byGroup.get(e.group) ?? []
        list.push(e)
        byGroup.set(e.group, list)
      }
      return [...byGroup.entries()]
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([group, items]) => ({ group, items: items.sort((a, b) => a.name.localeCompare(b.name)) }))
    })
    const overriddenCount = computed(() => entries.value.filter(e => e.set).length)

    return () => (
      <div class="settings-card">
        <div class="settings-card-title">高级配置（只读）</div>
        <div class="settings-card-sub">
          所有 AGENTGO_* 环境开关的当前生效值。修改方式：设置环境变量后重启应用；带「已覆盖」标记的是偏离默认值的项。
        </div>
        <div class="settings-actions">
          <button class="btn-secondary" onClick={toggle}>{open.value ? '收起' : '展开'}</button>
          {open.value && (
            <>
              <button class="btn-secondary" disabled={loading.value} onClick={load}>{loading.value ? '刷新中…' : '刷新'}</button>
              <label class="form-label" style={{ display: 'inline-flex', gap: '6px', alignItems: 'center' }}>
                <input type="checkbox" checked={onlyOverridden.value} onChange={(e: Event) => { onlyOverridden.value = (e.target as HTMLInputElement).checked }} />
                只看已覆盖（{overriddenCount.value}）
              </label>
            </>
          )}
        </div>
        {error.value && <div class="settings-card-sub">读取失败：{error.value}</div>}
        {open.value && groups.value.map(g => (
          <div key={g.group} style={{ marginTop: '12px' }}>
            <div class="form-label">{GROUP_LABELS[g.group] || g.group}</div>
            <table class="adv-config-table" style={{ width: '100%', fontSize: '12px', borderCollapse: 'collapse' }}>
              <tbody>
                {g.items.map(e => (
                  <tr key={e.name} style={{ borderTop: '1px solid var(--border, rgba(128,128,128,.2))' }}>
                    <td style={{ padding: '4px 8px 4px 0', whiteSpace: 'nowrap', fontFamily: 'monospace' }}>{e.name}</td>
                    <td style={{ padding: '4px 8px', fontFamily: 'monospace' }}>
                      {e.value === '' ? <em>（空）</em> : e.value}
                      {e.set && <span class="provider-preset-tag" style={{ marginLeft: '6px' }}>已覆盖</span>}
                    </td>
                    <td style={{ padding: '4px 0', opacity: 0.75 }}>{e.doc}{e.set && e.default !== '' ? `（默认 ${e.default}）` : ''}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ))}
      </div>
    )
  },
})
