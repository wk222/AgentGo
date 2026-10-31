import { computed, defineComponent, onBeforeUnmount, onMounted, ref, watch, type PropType } from 'vue'
import { wailsCall } from '../../shared/wails'
import type { MemoryGraphData, MemoryGraphNode } from '../../shared/ipc/types'
import {
  edgeStyle, emptyHint, fitViewport, graphNotice, KIND_ORDER, kindColor, kindLabel, layoutGraph, neighborIds, placeLabels, zoomAt, type Placed, type Viewport,
} from './memoryGraphModel'
import type { TreeAttach, TreeScopeOption } from './MemoryTreeView'
import './memory-graph.css'

const errText = (e: any) => String(e?.message || e || '未知错误')
const LIMITS = [30, 80, 150, 300]

/**
 * 记忆图谱：记忆之间的关联。总览画关联最多的那些，邻域从某条记忆向外走 1–3 跳。
 * 同一类型聚成一组；点分组标题只看这一类，点「全部类型」回到全图。只读，不改记忆。
 */
export default defineComponent({
  name: 'MemoryGraphView',
  props: {
    scope: { type: String, required: true },
    scopeOptions: { type: Array as PropType<TreeScopeOption[]>, default: () => [] },
    onScopeChange: { type: Function as PropType<(scope: string) => void>, default: undefined },
    onAttach: { type: Function as PropType<(record: TreeAttach) => void>, default: undefined },
    /** 从记忆列表点「Open graph」带进来的中心；变化时自动切到邻域。 */
    focusId: { type: String, default: '' },
  },
  setup(props) {
    const mode = ref<'overview' | 'ego'>('overview')
    const center = ref('')
    const depth = ref(1)
    const limit = ref(80)
    const kinds = ref<string[]>([])
    const data = ref<MemoryGraphData | null>(null)
    const busy = ref(false)
    const error = ref('')
    const selected = ref('')
    const hover = ref('')
    const view = ref<Viewport>({ x: 0, y: 0, k: 1 })
    const box = ref({ w: 640, h: 420 })
    const stage = ref<HTMLElement | null>(null)

    let gen = 0
    let observer: ResizeObserver | null = null

    const layout = computed(() => (data.value ? layoutGraph(data.value) : null))
    const nodeById = computed(() => new Map((data.value?.nodes || []).map(n => [n.id, n])))
    const focusSet = computed(() => {
      const id = hover.value || selected.value
      if (!id || !data.value) return null
      const s = neighborIds(data.value.edges, id)
      s.add(id)
      return s
    })
    const selectedNode = computed<MemoryGraphNode | null>(() => nodeById.value.get(selected.value) || null)
    const knownKinds = computed(() => {
      const present = Object.keys(data.value?.meta.groups || {})
      const all = [...KIND_ORDER, ...present.filter(k => !KIND_ORDER.includes(k))]
      return all.filter(k => KIND_ORDER.includes(k) || present.includes(k) || kinds.value.includes(k))
    })

    const fit = () => {
      if (layout.value) view.value = fitViewport(layout.value.bounds, box.value.w, box.value.h)
    }

    const load = async () => {
      const mine = ++gen
      busy.value = true
      error.value = ''
      try {
        const r = await wailsCall('GetMemoryGraphView', props.scope, mode.value, center.value, depth.value, kinds.value.join(','), limit.value)
        if (mine !== gen) return // 切换范围或条件后迟到的回复，丢弃
        if (!r?.success || !r.graph) {
          if (r?.not_found && mode.value === 'ego') {
            mode.value = 'overview'
            center.value = ''
            error.value = String(r.error || '这条记忆不在当前范围内，已回到总览')
            busy.value = false
            void load()
            return
          }
          throw new Error(String(r?.error || '读取记忆图谱失败'))
        }
        data.value = r.graph
        if (!r.graph.nodes.some((n: MemoryGraphNode) => n.id === selected.value)) selected.value = ''
        fit()
      } catch (e) {
        if (mine === gen) error.value = errText(e)
      } finally {
        if (mine === gen) busy.value = false
      }
    }

    const measure = () => {
      const el = stage.value
      if (!el) return
      const w = Math.max(240, Math.floor(el.clientWidth))
      const h = Math.max(240, Math.floor(el.clientHeight))
      if (w !== box.value.w || h !== box.value.h) {
        box.value = { w, h }
        fit()
      }
    }

    onMounted(() => {
      measure()
      if (typeof ResizeObserver !== 'undefined' && stage.value) {
        observer = new ResizeObserver(measure)
        observer.observe(stage.value)
      }
      if (props.focusId) openEgo(props.focusId)
      else void load()
    })
    onBeforeUnmount(() => { gen++; observer?.disconnect() })

    watch(() => props.scope, () => {
      selected.value = ''
      if (mode.value === 'ego') { mode.value = 'overview'; center.value = '' }
      void load()
    })
    watch(() => props.focusId, id => { if (id) openEgo(id) })

    function openEgo(id: string) {
      mode.value = 'ego'
      center.value = id
      selected.value = id
      void load()
    }
    const backToOverview = () => { mode.value = 'overview'; center.value = ''; void load() }
    const setDepth = (d: number) => { depth.value = d; if (mode.value === 'ego') void load() }
    const setLimit = (n: number) => { limit.value = n; void load() }
    const toggleKind = (k: string) => {
      kinds.value = kinds.value.includes(k) ? kinds.value.filter(x => x !== k) : [...kinds.value, k]
      void load()
    }
    const focusKind = (k: string) => { kinds.value = kinds.value.length === 1 && kinds.value[0] === k ? [] : [k]; void load() }
    const allKinds = () => { kinds.value = []; void load() }

    // 拖动平移、滚轮缩放。指针被捕获后，拖出画布也不会丢事件。
    let drag: { x: number; y: number; vx: number; vy: number; moved: boolean } | null = null
    const onDown = (e: PointerEvent) => {
      if (e.button !== 0) return
      drag = { x: e.clientX, y: e.clientY, vx: view.value.x, vy: view.value.y, moved: false }
      ;(e.currentTarget as Element).setPointerCapture?.(e.pointerId)
    }
    const onMove = (e: PointerEvent) => {
      if (!drag) return
      const dx = e.clientX - drag.x
      const dy = e.clientY - drag.y
      if (Math.abs(dx) + Math.abs(dy) > 3) drag.moved = true
      view.value = { ...view.value, x: drag.vx + dx, y: drag.vy + dy }
    }
    const onUp = () => {
      // 没有拖动的点击落在空白处：取消选中
      if (drag && !drag.moved) selected.value = ''
      drag = null
    }
    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      const r = (e.currentTarget as Element).getBoundingClientRect()
      view.value = zoomAt(view.value, e.clientX - r.left, e.clientY - r.top, e.deltaY < 0 ? 1.15 : 1 / 1.15)
    }
    const zoomBy = (f: number) => { view.value = zoomAt(view.value, box.value.w / 2, box.value.h / 2, f) }

    const attach = () => {
      const n = selectedNode.value
      if (n) props.onAttach?.({ id: n.id, content: n.title, scope: n.scope })
    }

    // 标题按位置避让：靠得近的节点放不下就缩短，再放不下就不显示；选中/悬停/中心的一定显示。
    const labelsFor = (nodes: MemoryGraphNode[], positions: Record<string, Placed>, k: number): Record<string, string> => {
      const always = new Set([selected.value, hover.value, center.value].filter(Boolean))
      const shown = k >= 0.7 ? nodes : nodes.filter(n => always.has(n.id))
      return placeLabels(shown, positions, always)
    }

    return () => {
      const d = data.value
      const L = layout.value
      const meta = d?.meta
      const filtered = kinds.value.length > 0
      const labels = L && d ? labelsFor(d.nodes, L.positions, view.value.k) : {}
      const notice = graphNotice(meta)
      const fs = focusSet.value
      return (
        <div class="mgraph">
          <div class="mgraph-bar">
            <div class="mgraph-seg" role="group" aria-label="视图">
              <button class={mode.value === 'overview' ? 'on' : ''} disabled={busy.value} onClick={backToOverview}>总览</button>
              <button class={mode.value === 'ego' ? 'on' : ''} disabled={!selected.value && mode.value !== 'ego'} title="先点选一个节点，再看它的邻域"
                onClick={() => (mode.value === 'ego' ? void load() : openEgo(selected.value))}>邻域</button>
            </div>
            {mode.value === 'ego' && (
              <div class="mgraph-seg" role="group" aria-label="跳数">
                {[1, 2, 3].map(n => <button class={depth.value === n ? 'on' : ''} onClick={() => setDepth(n)}>{n} 跳</button>)}
              </div>
            )}
            {props.scopeOptions.length > 1 && (
              <select class="mgraph-select" value={props.scope} onChange={(e: Event) => props.onScopeChange?.((e.target as HTMLSelectElement).value)}>
                {props.scopeOptions.map(o => <option value={o.value}>{o.label}</option>)}
              </select>
            )}
            <select class="mgraph-select" value={String(limit.value)} onChange={(e: Event) => setLimit(Number((e.target as HTMLSelectElement).value))} title="最多显示的节点数">
              {LIMITS.map(n => <option value={String(n)}>最多 {n} 个</option>)}
            </select>
            <span class="mgraph-grow" />
            <button class="mgraph-btn" onClick={() => zoomBy(1.25)} aria-label="放大">＋</button>
            <button class="mgraph-btn" onClick={() => zoomBy(0.8)} aria-label="缩小">－</button>
            <button class="mgraph-btn" onClick={fit}>适配</button>
            <button class="mgraph-btn" disabled={busy.value} onClick={() => void load()}>{busy.value ? '读取中…' : '刷新'}</button>
          </div>

          <div class="mgraph-kinds" role="group" aria-label="类型过滤">
            <button class={`mgraph-kind${filtered ? '' : ' on'}`} onClick={allKinds}>全部类型</button>
            {knownKinds.value.map(k => (
              <button class={`mgraph-kind${kinds.value.includes(k) ? ' on' : ''}`} onClick={() => toggleKind(k)}>
                <i style={{ background: kindColor(k) }} />{kindLabel(k)}{meta?.groups?.[k] ? <em>{meta.groups[k]}</em> : null}
              </button>
            ))}
          </div>

          {error.value && <div class="mgraph-error" role="alert">{error.value}</div>}
          {notice && <div class={`mgraph-notice${meta?.truncated ? ' warn' : ''}`} role="status">{notice}</div>}

          <div class="mgraph-body">
            <div class="mgraph-stage" ref={stage}>
              {L && d && d.nodes.length > 0 ? (
                <svg width={box.value.w} height={box.value.h} role="img" aria-label={`记忆图谱，${d.nodes.length} 个节点`}
                  onPointerdown={onDown} onPointermove={onMove} onPointerup={onUp} onPointercancel={onUp} onWheel={onWheel}>
                  <g transform={`translate(${view.value.x} ${view.value.y}) scale(${view.value.k})`}>
                    {L.clusters.map(c => (
                      <g class="mgraph-cluster" key={c.kind}>
                        <circle cx={c.x} cy={c.y} r={c.r} fill={kindColor(c.kind)} fill-opacity="0.07" stroke={kindColor(c.kind)} stroke-opacity="0.35" stroke-dasharray="4 5" />
                        <text class="mgraph-caption" x={c.x} y={c.y - c.r - 8} text-anchor="middle" fill={kindColor(c.kind)}
                          onPointerdown={(e: Event) => e.stopPropagation()} onClick={() => focusKind(c.kind)}>
                          {c.label} · {c.count}{kinds.value.length === 1 ? '（点此返回全部）' : ''}
                        </text>
                      </g>
                    ))}
                    {d.edges.map(e => {
                      const a = L.positions[e.source]
                      const b = L.positions[e.target]
                      if (!a || !b) return null
                      const st = edgeStyle(e)
                      const dim = fs ? !(fs.has(e.source) && fs.has(e.target)) : false
                      return (
                        <line key={`${e.source}>${e.target}`} x1={a.x} y1={a.y} x2={b.x} y2={b.y} stroke="currentColor"
                          stroke-width={st.width / Math.max(view.value.k, 0.4)} stroke-dasharray={st.dash || undefined}
                          stroke-opacity={dim ? 0.07 : 0.4}><title>{e.relation || 'related'}</title></line>
                      )
                    })}
                    {d.nodes.map(n => {
                      const p = L.positions[n.id]
                      if (!p) return null
                      const dim = fs ? !fs.has(n.id) : false
                      const label = labels[n.id]
                      return (
                        <g key={n.id} class="mgraph-node" tabindex={0} role="button" aria-label={`${kindLabel(n.modality)}：${n.title}`}
                          opacity={dim ? 0.25 : 1} transform={`translate(${p.x} ${p.y})`}
                          onPointerdown={(e: Event) => e.stopPropagation()}
                          onClick={() => { selected.value = n.id }} onDblclick={() => openEgo(n.id)}
                          onKeydown={(e: KeyboardEvent) => { if (e.key === 'Enter') selected.value = n.id }}
                          onPointerenter={() => { hover.value = n.id }} onPointerleave={() => { if (hover.value === n.id) hover.value = '' }}>
                          <circle r={p.r} fill={kindColor(n.modality)} stroke={n.id === selected.value ? 'var(--text-strong)' : n.id === center.value ? 'var(--accent)' : 'var(--surface)'} stroke-width={n.id === selected.value || n.id === center.value ? 3 : 1.5} />
                          {n.canonical && <circle r={p.r + 4} fill="none" stroke="var(--warning)" stroke-width="1.5" />}
                          {label && <text class="mgraph-label" y={p.r + 13} text-anchor="middle">{label}</text>}
                        </g>
                      )
                    })}
                  </g>
                </svg>
              ) : (
                <div class="mgraph-empty">{busy.value ? '读取中…' : emptyHint(meta, filtered)}</div>
              )}
              {busy.value && d && <div class="mgraph-busy">更新中…</div>}
            </div>

            <aside class="mgraph-side" aria-live="polite">
              {selectedNode.value ? (
                <div>
                  <div class="mgraph-side-kind" style={{ color: kindColor(selectedNode.value.modality) }}>{kindLabel(selectedNode.value.modality)}{selectedNode.value.canonical ? ' · 权威' : ''}</div>
                  <div class="mgraph-side-title">{selectedNode.value.title}</div>
                  <dl class="mgraph-facts">
                    <dt>关联</dt><dd>{selectedNode.value.degree} 条</dd>
                    <dt>范围</dt><dd>{selectedNode.value.scope || 'global'}</dd>
                    <dt>重要度</dt><dd>{Number(selectedNode.value.importance || 0).toFixed(2)}</dd>
                    <dt>编号</dt><dd class="mono">{selectedNode.value.id}</dd>
                  </dl>
                  <div class="mgraph-side-actions">
                    {selectedNode.value.id !== center.value && <button class="mgraph-btn primary" onClick={() => openEgo(selectedNode.value!.id)}>以它为中心展开</button>}
                    {mode.value === 'ego' && <button class="mgraph-btn" onClick={backToOverview}>返回总览</button>}
                    {props.onAttach && <button class="mgraph-btn" onClick={attach}>加入对话</button>}
                  </div>
                </div>
              ) : (
                <div class="mgraph-side-hint">
                  点一个节点看详情；双击或点「以它为中心展开」查看它的邻域。<br />
                  圈的大小表示关联数，虚线表示「取代」关系，金色外圈是权威记忆。<br />
                  拖动画布平移，滚轮缩放。
                </div>
              )}
            </aside>
          </div>
        </div>
      )
    }
  },
})
