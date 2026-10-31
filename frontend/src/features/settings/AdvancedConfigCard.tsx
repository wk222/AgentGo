import { computed, defineComponent, onMounted, ref } from 'vue'
import { RefreshCw, Search, SlidersHorizontal } from 'lucide-vue-next'
import { wailsCall } from '../../shared/wails'
import type { ConfigEntry } from '../../shared/ipc/types'

const GROUP_LABELS: Record<string, string> = { model: '模型', agent: 'Agent', memory: '记忆', matrix: '应用矩阵', gateway: '网关', plugins: '工具与插件', sandbox: '沙箱', workflow: '工作流', debug: '调试' }

export default defineComponent({
 name: 'AdvancedConfigCard',
 setup() {
  const loading = ref(false)
  const error = ref('')
  const entries = ref<ConfigEntry[]>([])
  const search = ref('')
  const group = ref('all')
  const overridden = ref(false)
  const load = async () => {
   loading.value = true; error.value = ''
   try { entries.value = (await wailsCall('GetConfigSnapshot')) || [] }
   catch (e) { error.value = e instanceof Error ? e.message : String(e) }
   finally { loading.value = false }
  }
  const groupNames = computed(() => [...new Set(entries.value.map(e => e.group))].sort())
  const overriddenCount = computed(() => entries.value.filter(e => e.set).length)
  const filtered = computed(() => entries.value.filter(e => {
   const query = search.value.trim().toLowerCase()
   return (!overridden.value || e.set) && (group.value === 'all' || e.group === group.value) && (!query || (e.name + ' ' + e.doc).toLowerCase().includes(query))
  }).sort((a, b) => a.name.localeCompare(b.name)))
  const displayValue = (entry: ConfigEntry) => entry.secret ? (entry.value ? '已配置 · 已隐藏' : '未配置') : entry.value === '' ? '空值' : entry.value
  onMounted(load)
  return () => <section class="settings-surface settings-advanced"><div class="settings-surface-heading"><div><h3>运行参数</h3><p>此处只读。修改环境变量后重启应用，即可更新对应配置。</p></div><button class="settings-icon-button" disabled={loading.value} aria-label="刷新高级配置" onClick={load}><RefreshCw size={15} class={loading.value ? 'settings-spin' : ''} /></button></div><div class="settings-config-toolbar"><div class="settings-config-search"><Search size={15} /><input aria-label="搜索配置" placeholder="搜索参数名称或说明…" value={search.value} onInput={(e: any) => { search.value = e.target.value }} /></div><button class={['settings-config-filter', overridden.value ? 'active' : '']} role="switch" aria-checked={overridden.value} onClick={() => { overridden.value = !overridden.value }}><SlidersHorizontal size={13} />仅看已覆盖<span>{overriddenCount.value}</span></button></div><div class="settings-config-groups"><button class={group.value === 'all' ? 'active' : ''} onClick={() => { group.value = 'all' }}>全部</button>{groupNames.value.map(name => <button key={name} class={group.value === name ? 'active' : ''} onClick={() => { group.value = name }}>{GROUP_LABELS[name] || name}</button>)}</div>{error.value && <div class="settings-feedback error" role="status">{error.value}</div>}<div class="settings-config-count">{loading.value ? '正在读取配置…' : filtered.value.length + ' 项配置'}</div><div class="settings-config-list">{filtered.value.map(entry => <div class="settings-config-row" key={entry.name}><div class="settings-config-name"><code>{entry.name}</code><div><span>{GROUP_LABELS[entry.group] || entry.group}</span>{entry.set && <span class="settings-soft-tag">已覆盖</span>}</div><p>{entry.doc}</p></div><div class="settings-config-value"><code>{displayValue(entry)}</code>{entry.set && !entry.secret && <small>默认：{entry.default || '空值'}</small>}</div></div>)}</div>{!loading.value && !filtered.value.length && !error.value && <div class="settings-empty"><Search size={24} /><strong>没有匹配的配置</strong><span>尝试其他关键词，或取消筛选条件。</span></div>}</section>
 },
})
