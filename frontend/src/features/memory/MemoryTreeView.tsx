import { computed, defineComponent, nextTick, onBeforeUnmount, onMounted, ref, watch, type PropType } from 'vue'
import { wailsCall } from '../../shared/wails'
import type { MemoryTreeNode, MemoryTreeOverview } from '../../shared/ipc/types'
import { barPercent, budgetText, childrenOf, coverageText, mergeChain, nodeTitle, summarizedPercent, treeNotice } from './memoryTreeModel'
import './memory-tree.css'

export interface TreeScopeOption { value: string; label: string }
export interface TreeAttach { id: string; content: string; scope: string }

interface Preview {
  query: string
  prompt: string
  nodes: MemoryTreeNode[]
  tokens: number
  budget: number
  hierarchy: boolean
}

const errText = (e: any) => String(e?.message || e || '未知错误')
const timeText = (ts?: number) => (ts ? new Date(ts * 1000).toLocaleString() : '')

/**
 * 记忆树：把模型自动召回时导航的那棵分层摘要树画出来。
 * 节点按需展开（每次只取直接子节点），「模型视角」用同一条自动召回路径预览模型会读到哪些节点。
 */
export default defineComponent({
  name: 'MemoryTreeView',
  props: {
    scope: { type: String, required: true },
    scopeOptions: { type: Array as PropType<TreeScopeOption[]>, default: () => [] },
    onScopeChange: { type: Function as PropType<(scope: string) => void>, default: undefined },
    onAttach: { type: Function as PropType<(record: TreeAttach) => void>, default: undefined },
  },
  setup(props) {
    const overview = ref<MemoryTreeOverview | null>(null)
    const hierarchyOn = ref(true)
    const loaded = ref<Record<string, MemoryTreeNode>>({})
    const expanded = ref<Record<string, boolean>>({})
    const loading = ref<Record<string, boolean>>({})
    const busy = ref<'' | 'load' | 'refresh' | 'preview'>('')
    const error = ref('')
    const task = ref('')
    const budget = ref(0)
    const preview = ref<Preview | null>(null)
    const flash = ref('')
    const hot = computed(() => new Set((preview.value?.nodes || []).map(n => n.id)))

    // 切换范围或卸载后，迟到的回复一律丢弃。
    let gen = 0
    let pollTimer = 0
    let polls = 0
    const stopPoll = () => {
      if (pollTimer) { window.clearTimeout(pollTimer); pollTimer = 0 }
    }
    const schedulePoll = () => {
      stopPoll()
      if (!overview.value?.refreshing || polls >= 40) return
      polls++
      pollTimer = window.setTimeout(() => { void load(true) }, 3000)
    }

    const fetchNode = async (id: string): Promise<MemoryTreeNode | null> => {
      const r = await wailsCall('GetMemoryTree', props.scope, id)
      return r?.success && r.node ? r.node : null
    }

    // 后台补写摘要之后重读：保留还在树里的展开节点，ID 已不存在的自动丢弃。
    const reopen = async (mine: number, root?: MemoryTreeNode) => {
      const rootOpen = root ? expanded.value[root.id] !== false : false
      const ids = Object.keys(expanded.value).filter(id => expanded.value[id] && id !== root?.id)
      const fresh: Record<string, MemoryTreeNode> = root ? { [root.id]: root } : {}
      const open: Record<string, boolean> = root ? { [root.id]: rootOpen } : {}
      await Promise.all(ids.map(async id => {
        const node = await fetchNode(id).catch(() => null)
        if (node) { fresh[id] = node; open[id] = true }
      }))
      if (mine !== gen) return
      loaded.value = fresh
      expanded.value = open
    }

    const load = async (keep = false) => {
      const mine = ++gen
      busy.value = 'load'
      error.value = ''
      try {
        const r = await wailsCall('GetMemoryTree', props.scope, '')
        if (mine !== gen) return
        if (!r?.success || !r.overview) throw new Error(String(r?.error || '读取记忆树失败'))
        hierarchyOn.value = r.hierarchy !== false
        overview.value = r.overview
        const root = r.overview.root
        if (keep) {
          await reopen(mine, root)
        } else {
          loaded.value = root ? { [root.id]: root } : {}
          expanded.value = root ? { [root.id]: true } : {}
          preview.value = null
          polls = 0
        }
      } catch (e) {
        if (mine === gen) error.value = errText(e)
      } finally {
        if (mine === gen) { busy.value = ''; schedulePoll() }
      }
    }

    const refresh = async () => {
      busy.value = 'refresh'
      error.value = ''
      polls = 0
      try {
        const r = await wailsCall('RefreshMemoryTree', props.scope)
        if (!r?.success) throw new Error(String(r?.error || '整理失败'))
        await load(true)
      } catch (e) {
        error.value = errText(e)
      } finally {
        if (busy.value === 'refresh') busy.value = ''
      }
    }

    const toggle = async (n: MemoryTreeNode) => {
      if (expanded.value[n.id]) {
        expanded.value = { ...expanded.value, [n.id]: false }
        return
      }
      if (!n.record_id && childrenOf(n, loaded.value).length === 0 && n.child_count > 0) {
        loading.value = { ...loading.value, [n.id]: true }
        try {
          const full = await fetchNode(n.id)
          if (!full) throw new Error('这个节点已经变化，请先点「整理摘要树」或刷新')
          loaded.value = { ...loaded.value, [n.id]: full }
        } catch (e) {
          error.value = errText(e)
          return
        } finally {
          loading.value = { ...loading.value, [n.id]: false }
        }
      }
      expanded.value = { ...expanded.value, [n.id]: true }
    }

    const locate = async (id: string) => {
      error.value = ''
      try {
        const r = await wailsCall('LocateMemoryTreeNode', props.scope, id)
        if (!r?.success || !r.chain?.length) throw new Error(String(r?.error || '没有在树里找到这个节点'))
        const next = mergeChain({ loaded: loaded.value, expanded: expanded.value }, r.chain)
        loaded.value = next.loaded
        expanded.value = next.expanded
        flash.value = id
        await nextTick()
        document.getElementById(`mtn-${id}`)?.scrollIntoView({ block: 'center', behavior: 'smooth' })
        window.setTimeout(() => { if (flash.value === id) flash.value = '' }, 1800)
      } catch (e) {
        error.value = errText(e)
      }
    }

    const runPreview = async () => {
      if (!task.value.trim()) {
        error.value = '先写一句要让智能体做的事，例如「把上月出差报销按公司口径整理成表」'
        return
      }
      busy.value = 'preview'
      error.value = ''
      try {
        const r = await wailsCall('PreviewMemoryContext', props.scope, task.value, Number(budget.value) || 0)
        if (!r?.success) throw new Error(String(r?.error || '预览失败'))
        preview.value = {
          query: task.value.trim(), prompt: r.prompt || '', nodes: r.nodes || [],
          tokens: r.tokens || 0, budget: r.budget || 0, hierarchy: r.hierarchy !== false,
        }
      } catch (e) {
        error.value = errText(e)
      } finally {
        busy.value = ''
      }
    }

    watch(() => props.scope, () => { stopPoll(); polls = 0; void load(false) })
    onMounted(() => { void load(false) })
    onBeforeUnmount(() => { gen++; stopPoll() })

    const stateLabel = (ov: MemoryTreeOverview) =>
      ov.reason === 'empty' ? '无记忆' : ov.refreshing ? '整理中' : ov.stale ? '待更新' : ov.ready ? '最新' : '未建立'

    const renderNode = (n: MemoryTreeNode, parentCount: number): any => {
      const isLeaf = !!n.record_id
      const open = !!expanded.value[n.id]
      const kids = isLeaf ? [] : childrenOf(n, loaded.value)
      const sketch = !isLeaf && !n.complete
      const rec = n.record
      const pct = barPercent(n.count, parentCount)
      const onToggle = () => (isLeaf
        ? (expanded.value = { ...expanded.value, [n.id]: !open })
        : void toggle(n))
      return (
        <div key={n.id} id={`mtn-${n.id}`} class={['mtree-node', hot.value.has(n.id) && 'hot', flash.value === n.id && 'flash']}>
          <div class="mtree-row">
            <button class="mtree-toggle" aria-expanded={open} aria-label={open ? '收起' : '展开'} onClick={onToggle}>
              {loading.value[n.id] ? '…' : open ? '▾' : '▸'}
            </button>
            <span class="mtree-title" title={nodeTitle(n)}>{nodeTitle(n)}</span>
            <span class={['mtree-meter', sketch && 'sketch']} title={`覆盖 ${n.count} 条，占上一层的 ${pct}%`}>
              <i style={{ width: `${pct}%` }} />
            </span>
            <span class="mtree-count">{n.count} 条</span>
            {!isLeaf && (
              <span class={['mtree-tag', !sketch && 'model']} title={sketch ? '直接节选子节点的文字（模型还没写摘要，或没有可用的模型）' : '由模型写的概括'}>
                {sketch ? '节选' : '模型摘要'}
              </span>
            )}
            {!isLeaf && n.level > 0 && <span class="mtree-tag" title="离记忆原文有几层">L{n.level}</span>}
            {rec?.canonical && <span class="mtree-tag star">canonical</span>}
            {hot.value.has(n.id) && <span class="mtree-tag seen">模型可见</span>}
          </div>
          <div class={['mtree-gist', isLeaf && open && 'full']}>{isLeaf && open ? (rec?.content || n.summary) : n.summary}</div>
          {isLeaf && open && (
            <div class="mtree-gist-actions">
              <span>{[rec?.scope, rec?.kind, rec ? `重要度 ${rec.importance.toFixed(2)}` : '', timeText(rec?.created_at)].filter(Boolean).join(' · ')}</span>
              {props.onAttach && (
                <button class="mtree-btn link" onClick={() => props.onAttach?.({ id: n.record_id as string, content: rec?.content || n.summary, scope: rec?.scope || props.scope })}>
                  加入对话
                </button>
              )}
            </div>
          )}
          {open && kids.length > 0 && <div class="mtree-kids">{kids.map(c => renderNode(c, n.count))}</div>}
        </div>
      )
    }

    return () => {
      const ov = overview.value
      const notice = treeNotice(ov, hierarchyOn.value)
      const root = ov?.root
      const pv = preview.value
      return (
        <div class="mtree">
          <div class="mtree-bar-row">
            {props.scopeOptions.length > 1 && (
              <div class="mtree-seg">
                {props.scopeOptions.map(o => (
                  <button key={o.value} class={o.value === props.scope && 'on'} onClick={() => props.onScopeChange?.(o.value)}>{o.label}</button>
                ))}
              </div>
            )}
            <span class="mtree-grow" />
            <button class="mtree-btn" disabled={busy.value !== ''} onClick={() => load(true)}>刷新</button>
            <button class="mtree-btn primary" disabled={busy.value !== ''} onClick={refresh}>{busy.value === 'refresh' ? '整理中…' : '整理摘要树'}</button>
          </div>

          {error.value && <div class="mtree-error" role="alert">{error.value}</div>}
          {notice && <div class={['mtree-notice', notice.tone]} role="status">{notice.text}</div>}

          {ov && (
            <div class="mtree-stats">
              <div class="mtree-stat"><span>记忆条数</span><strong>{ov.records}</strong><small>范围 {ov.scope}（含全局）</small></div>
              <div class="mtree-stat"><span>已纳入树</span><strong>{coverageText(ov)}</strong><small>状态：{stateLabel(ov)}</small></div>
              <div class="mtree-stat"><span>层数</span><strong>{ov.ready || ov.stale ? ov.levels : '—'}</strong><small>每个节点最多 {ov.fanout} 个子节点</small></div>
              <div class="mtree-stat"><span>分支节点</span><strong>{ov.branches}</strong><small>模型摘要 {summarizedPercent(ov)}%</small></div>
            </div>
          )}

          <section class="mtree-card">
            <header>模型视角 <small>输入一句任务，看看自动召回时模型会读到树的哪些部分</small></header>
            <div class="mtree-card-body">
              <div class="mtree-ask">
                <input class="q" value={task.value} placeholder="例如：把上月出差报销按公司口径整理成表"
                  onInput={(e: Event) => { task.value = (e.target as HTMLInputElement).value }}
                  onKeydown={(e: KeyboardEvent) => { if (e.key === 'Enter') void runPreview() }} />
                <input class="n" type="number" min="0" step="100" value={budget.value || ''} placeholder="Token 预算"
                  title="留空 = 用「自动注入 Token」设置"
                  onInput={(e: Event) => { budget.value = Number((e.target as HTMLInputElement).value) || 0 }} />
                <button class="mtree-btn primary" disabled={busy.value === 'preview'} onClick={runPreview}>{busy.value === 'preview' ? '预览中…' : '预览模型会读到什么'}</button>
              </div>
              {pv && (
                <div>
                  <div class="mtree-hint">
                    用了 {budgetText(pv.tokens, pv.budget)} Token，模型读到 {pv.nodes.length} 个摘要树节点。
                    {!pv.hierarchy && '「分层摘要树」已关闭，这里是按相关度平铺的记忆。'}
                    {pv.hierarchy && pv.nodes.length === 0 && '没有用到摘要树节点：记忆较少时直接列出原文，或树还没建立。'}
                  </div>
                  {pv.nodes.length > 0 && (
                    <div class="mtree-seen">
                      {pv.nodes.map(n => (
                        <span key={n.id} class="mtree-chip">
                          {nodeTitle(n)} <em>{n.count} 条</em>
                          <button class="mtree-btn link" onClick={() => locate(n.id)}>在树中定位</button>
                        </span>
                      ))}
                    </div>
                  )}
                  <details>
                    <summary class="mtree-hint">查看注入给模型的原文</summary>
                    <pre class="mtree-prompt">{pv.prompt || '（空：这个范围没有可注入的记忆）'}</pre>
                  </details>
                </div>
              )}
            </div>
          </section>

          <section class="mtree-card">
            <header>摘要树 <small>条形 = 占上一层的记忆比例；灰色为节选，彩色为模型摘要；▸ 展开子节点</small></header>
            <div class="mtree-tree">
              {root
                ? renderNode(root, root.count)
                : <div class="mtree-empty">{busy.value === 'load' ? '读取中…' : ov?.reason === 'empty' ? '这个范围里还没有记忆。' : '还没有摘要树。点上面的「整理摘要树」。'}</div>}
            </div>
          </section>
        </div>
      )
    }
  },
})
