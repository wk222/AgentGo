import { computed, defineComponent, onMounted, ref, watch, type PropType } from 'vue'
import { wailsCall } from '../wails'

const tabs = [
  { key: 'overview', label: '概览' },
  { key: 'recall', label: 'Recall QA' },
  { key: 'truth', label: 'Truth' },
  { key: 'graph', label: 'Graph View' },
] as const

const panelStyle = {
  display: 'flex',
  flexDirection: 'column' as const,
  height: '100%',
  minHeight: 0,
  overflow: 'hidden',
  background: 'var(--bg)',
}

const headerStyle = {
  padding: '16px 18px 12px',
  borderBottom: '1px solid var(--border-subtle)',
  flexShrink: 0,
}

const bodyStyle = {
  flex: 1,
  minHeight: 0,
  overflow: 'auto',
  padding: '14px 18px 18px',
}

const inputStyle = {
  minWidth: 0,
  border: '1px solid var(--border)',
  borderRadius: 'var(--radius-md)',
  background: 'var(--input-bg)',
  color: 'var(--text)',
  padding: '7px 9px',
  fontSize: '12px',
  outline: 'none',
}

const cardStyle = {
  border: '1px solid var(--border-subtle)',
  borderRadius: 'var(--radius-md)',
  background: 'var(--surface-2)',
  overflow: 'hidden',
}

const smallButton = (primary = false) => ({
  border: primary ? 'none' : '1px solid var(--border)',
  borderRadius: 'var(--radius-md)',
  background: primary ? 'var(--accent-bg)' : 'transparent',
  color: primary ? 'var(--accent-text)' : 'var(--text-dim)',
  padding: '6px 10px',
  fontSize: '12px',
  fontWeight: primary ? 700 : 500,
  cursor: 'pointer',
  whiteSpace: 'nowrap' as const,
})

function clipText(text: string, max = 260) {
  const s = String(text || '').trim()
  return s.length > max ? `${s.slice(0, max)}...` : s
}

function formatTime(ts: any) {
  const n = Number(ts || 0)
  if (!n) return ''
  try { return new Date(n * 1000).toLocaleString() } catch { return '' }
}

function fixed(n: any, digits = 2) {
  const v = Number(n || 0)
  return Number.isFinite(v) ? v.toFixed(digits) : '0.00'
}

export default defineComponent({
  name: 'MemoryWorkbenchPanel',
  props: {
    onSendToAgent: { type: Function as PropType<(text: string) => void>, default: undefined },
    sessionId: { type: String, default: '' },
  },
  setup(props) {
    const activeTab = ref<typeof tabs[number]['key']>('overview')
    const memories = ref<any[]>([])
    const query = ref('session context user preferences goals')
    const scope = ref(props.sessionId || 'global')
    const modality = ref('')
    const limit = ref(8)
    const loading = ref(false)
    const busy = ref('')
    const status = ref('')
    const result = ref<any | null>(null)

    const truthA = ref('')
    const truthB = ref('')
    const truthNewFact = ref('')
    const truthScope = ref(props.sessionId || 'global')
    const contradictions = ref<any[]>([])
    const truthResolution = ref<any | null>(null)

    const graphCenter = ref('')
    const graph = ref<any | null>(null)

    watch(() => props.sessionId, sid => {
      if (!scope.value || scope.value === 'global') scope.value = sid || 'global'
      if (!truthScope.value || truthScope.value === 'global') truthScope.value = sid || 'global'
    })

    const loadMemories = async () => {
      try {
        memories.value = (await wailsCall('ListMemories', 120)) || []
      } catch {}
    }

    const runQA = async () => {
      loading.value = true
      status.value = ''
      try {
        const r = await wailsCall('MemoryRecallQA', query.value, scope.value, modality.value, Number(limit.value || 8))
        result.value = r
        status.value = r?.success
          ? `Recall ${r.count || 0} items in ${r.elapsed_ms || 0} ms`
          : (r?.error || 'Recall failed')
        await loadMemories()
      } catch (e: any) {
        status.value = e?.message || 'Recall failed'
      } finally {
        loading.value = false
      }
    }

    const previewInject = async () => {
      busy.value = 'prompt'
      status.value = ''
      try {
        const r = await wailsCall('MemoryContextPrompt', scope.value || 'global')
        result.value = { ...(result.value || {}), success: r?.success, prompt: r?.prompt || '', prompt_error: r?.error }
        status.value = r?.success ? `Injection preview: ${r.scope || scope.value}` : (r?.error || 'Preview failed')
      } catch (e: any) {
        status.value = e?.message || 'Preview failed'
      } finally {
        busy.value = ''
      }
    }

    const distill = async () => {
      busy.value = 'distill'
      try {
        const r = await wailsCall('MemoryDistill', scope.value || 'global')
        status.value = r?.success ? (r.summary || 'Distill complete') : (r?.error || 'Distill failed')
        await loadMemories()
      } catch (e: any) {
        status.value = e?.message || 'Distill failed'
      } finally {
        busy.value = ''
      }
    }

    const runGC = async () => {
      busy.value = 'gc'
      try {
        const r = await wailsCall('MemoryGC')
        status.value = r?.success ? `GC archived ${r.archived || 0} memories` : (r?.error || 'GC failed')
        await loadMemories()
      } catch (e: any) {
        status.value = e?.message || 'GC failed'
      } finally {
        busy.value = ''
      }
    }

    const feedback = async (row: any, signal: 'positive' | 'negative' | 'disproved') => {
      busy.value = `${row.id}:${signal}`
      try {
        const r = await wailsCall('MemoryFeedback', row.id, signal)
        status.value = r?.success ? `Feedback saved: ${signal}` : (r?.error || 'Feedback failed')
        await loadMemories()
        if (result.value) await runQA()
      } catch (e: any) {
        status.value = e?.message || 'Feedback failed'
      } finally {
        busy.value = ''
      }
    }

    const selectTruthA = (id: string) => { truthA.value = id; activeTab.value = 'truth' }
    const selectGraph = (id: string) => { graphCenter.value = id; activeTab.value = 'graph'; void loadGraph() }

    const detectTruth = async () => {
      if (!truthNewFact.value.trim()) {
        status.value = '先输入新事实'
        return
      }
      busy.value = 'truth-detect'
      try {
        const r = await wailsCall('DetectMemoryContradictions', truthNewFact.value, truthScope.value || 'global')
        contradictions.value = Array.isArray(r) ? r : (r?.contradictions || [])
        status.value = `Detected ${contradictions.value.length} contradiction candidate(s)`
      } catch (e: any) {
        status.value = e?.message || 'Truth detection failed'
      } finally {
        busy.value = ''
      }
    }

    const resolveTruth = async () => {
      if (!truthA.value || !truthB.value) {
        status.value = '选择两条记忆后再解析'
        return null
      }
      busy.value = 'truth-resolve'
      try {
        const r = await wailsCall('ResolveMemoryTruth', truthA.value, truthB.value)
        truthResolution.value = r
        status.value = r?.resolution ? `Resolution: ${r.resolution}` : 'No resolution returned'
        return r
      } catch (e: any) {
        status.value = e?.message || 'Truth resolve failed'
        return null
      } finally {
        busy.value = ''
      }
    }

    const applyTruth = async () => {
      const res = truthResolution.value || await resolveTruth()
      if (!res) return
      busy.value = 'truth-apply'
      try {
        const r = await wailsCall('ApplyMemoryTruthResolution', truthA.value, truthB.value, JSON.stringify(res))
        status.value = r?.success ? `Applied: ${r.record?.id || r.resolution?.applied_record_id || ''}` : (r?.error || 'Apply failed')
        await loadMemories()
      } catch (e: any) {
        status.value = e?.message || 'Apply failed'
      } finally {
        busy.value = ''
      }
    }

    const resolveAndApply = async () => {
      if (!truthA.value || !truthB.value) {
        status.value = '选择两条记忆后再执行'
        return
      }
      busy.value = 'truth-full'
      try {
        const r = await wailsCall('ResolveAndApplyMemoryTruth', truthA.value, truthB.value)
        truthResolution.value = r?.resolution || null
        status.value = r?.success ? `Resolved and applied: ${r.record?.id || ''}` : (r?.error || 'Resolve/apply failed')
        await loadMemories()
      } catch (e: any) {
        status.value = e?.message || 'Resolve/apply failed'
      } finally {
        busy.value = ''
      }
    }

    async function loadGraph() {
      if (!graphCenter.value.trim()) {
        status.value = '输入或选择一条 memory id'
        return
      }
      busy.value = 'graph'
      try {
        const r = await wailsCall('GetMemoryGraph', graphCenter.value.trim())
        graph.value = r?.success ? r.graph : null
        status.value = r?.success ? `Graph loaded: ${graph.value?.nodes?.length || 0} nodes` : (r?.error || 'Graph failed')
      } catch (e: any) {
        status.value = e?.message || 'Graph failed'
      } finally {
        busy.value = ''
      }
    }

    const sendQA = () => {
      if (!result.value) return
      const rows = (result.value.records || []).slice(0, 6).map((r: any) =>
        `#${r.rank} ${r.id} score=${r.estimated_score} ${r.scope}/${r.modality}\n${clipText(r.content, 180)}\nwhy: ${r.why}`
      ).join('\n\n')
      props.onSendToAgent?.(`请基于这次 Memory Recall QA 判断记忆注入质量，并指出应该保留、降权、作废或补充哪些记忆。\n\nquery: ${result.value.query}\nscope: ${result.value.scope}\nengine: ${result.value.engine || ''}\n\n${rows}\n\nprompt preview:\n${clipText(result.value.prompt || '', 1200)}`)
    }

    const stats = computed(() => {
      const byModality: Record<string, number> = {}
      let importance = 0
      let canonical = 0
      let recalled = 0
      for (const m of memories.value) {
        const key = m.modality || 'memory'
        byModality[key] = (byModality[key] || 0) + 1
        importance += Number(m.importance || 0)
        if (m.is_canonical) canonical++
        if (Number(m.recall_count || 0) > 0) recalled++
      }
      return {
        total: memories.value.length,
        byModality,
        canonical,
        recalled,
        avgImportance: memories.value.length ? importance / memories.value.length : 0,
      }
    })

    onMounted(async () => {
      await loadMemories()
      await runQA()
    })

    const MemoryRow = (row: any, showSelect = false) => (
      <div key={row.id} style={cardStyle}>
        <div style={{ display: 'flex', gap: '8px', alignItems: 'center', padding: '9px 10px', borderBottom: '1px solid var(--border-subtle)' }}>
          <strong style={{ fontSize: '12px', color: 'var(--text-strong)' }}>{row.rank ? `#${row.rank}` : row.modality || 'memory'}</strong>
          <span style={{ fontSize: '11px', color: 'var(--text-dim)', fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{row.id}</span>
          {row.is_canonical ? <span style={{ fontSize: '11px', color: 'var(--accent-text)' }}>canonical</span> : null}
          <span style={{ marginLeft: 'auto', fontSize: '11px', color: 'var(--text-dim)' }}>{row.scope || 'global'}</span>
        </div>
        <div style={{ padding: '10px' }}>
          <div style={{ fontSize: '13px', lineHeight: 1.55, color: 'var(--text)' }}>{clipText(row.content, 520)}</div>
          <div style={{ marginTop: '8px', fontSize: '11px', color: 'var(--text-dim)', lineHeight: 1.5 }}>
            importance {fixed(row.importance)} · recall {row.recall_count || 0}
            {formatTime(row.last_recall_at) ? ` · last ${formatTime(row.last_recall_at)}` : ''}
            {row.why ? ` · ${row.why}` : ''}
            {row.supersedes_id ? ` · superseded by ${row.supersedes_id}` : ''}
            {row.contradicted_by ? ` · contradicted by ${row.contradicted_by}` : ''}
          </div>
          <div style={{ display: 'flex', gap: '6px', flexWrap: 'wrap', marginTop: '10px' }}>
            <button style={smallButton()} disabled={!!busy.value} onClick={() => feedback(row, 'positive')}>Useful</button>
            <button style={smallButton()} disabled={!!busy.value} onClick={() => feedback(row, 'negative')}>Downrank</button>
            <button style={smallButton()} disabled={!!busy.value} onClick={() => feedback(row, 'disproved')}>Forget</button>
            <button style={smallButton()} onClick={() => selectGraph(row.id)}>Open graph</button>
            {showSelect ? <button style={smallButton()} onClick={() => selectTruthA(row.id)}>Use as A</button> : null}
          </div>
        </div>
      </div>
    )

    return () => {
      const rows = result.value?.records || []
      const prompt = result.value?.prompt || ''
      return (
        <div style={panelStyle}>
          <div style={headerStyle}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '10px' }}>
              <span style={{ fontSize: '18px' }}>Memory</span>
              <strong style={{ fontSize: '15px', color: 'var(--text-strong)' }}>Workbench</strong>
              {result.value?.engine && <span style={{ marginLeft: 'auto', fontSize: '11px', color: 'var(--text-dim)' }}>{result.value.engine}</span>}
            </div>
            <div style={{ display: 'flex', gap: '6px', flexWrap: 'wrap' }}>
              {tabs.map(tab => (
                <button key={tab.key} style={smallButton(activeTab.value === tab.key)} onClick={() => activeTab.value = tab.key}>
                  {tab.label}
                </button>
              ))}
            </div>
          </div>

          <div style={bodyStyle}>
            {status.value && <div style={{ marginBottom: '12px', fontSize: '12px', color: result.value?.success === false ? 'var(--error)' : 'var(--text-dim)' }}>{status.value}</div>}

            {activeTab.value === 'overview' && (
              <div>
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, minmax(120px, 1fr))', gap: '8px', marginBottom: '12px' }}>
                  {[
                    ['Active memories', stats.value.total],
                    ['Canonical', stats.value.canonical],
                    ['Recalled', stats.value.recalled],
                    ['Avg importance', fixed(stats.value.avgImportance)],
                  ].map(([label, value]) => (
                    <div style={{ ...cardStyle, padding: '12px' }}>
                      <div style={{ fontSize: '11px', color: 'var(--text-dim)', marginBottom: '5px' }}>{label}</div>
                      <div style={{ fontSize: '20px', fontWeight: 700, color: 'var(--text-strong)' }}>{value}</div>
                    </div>
                  ))}
                </div>
                <div style={{ ...cardStyle, padding: '12px', marginBottom: '12px' }}>
                  <div style={{ fontSize: '12px', fontWeight: 700, marginBottom: '8px' }}>Modality mix</div>
                  <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap' }}>
                    {Object.entries(stats.value.byModality).map(([k, v]) => (
                      <span style={{ fontSize: '12px', color: 'var(--text-dim)', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', padding: '4px 8px' }}>{k}: {v}</span>
                    ))}
                    {!Object.keys(stats.value.byModality).length && <span style={{ color: 'var(--text-dim)', fontSize: '12px' }}>No active memories.</span>}
                  </div>
                </div>
                <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap', marginBottom: '12px' }}>
                  <button style={smallButton(true)} disabled={!!busy.value} onClick={distill}>Distill current scope</button>
                  <button style={smallButton()} disabled={!!busy.value} onClick={runGC}>Run GC</button>
                  <button style={smallButton()} onClick={loadMemories}>Refresh</button>
                </div>
                <div style={{ display: 'grid', gap: '8px' }}>
                  {memories.value.slice(0, 8).map(m => MemoryRow(m, true))}
                </div>
              </div>
            )}

            {activeTab.value === 'recall' && (
              <div>
                <div style={{ display: 'grid', gridTemplateColumns: 'minmax(180px, 1fr) 150px 120px 72px auto', gap: '8px', alignItems: 'center', marginBottom: '10px' }}>
                  <input style={inputStyle} value={query.value} placeholder="Recall query" onInput={(e: Event) => query.value = (e.target as HTMLInputElement).value} />
                  <input style={inputStyle} value={scope.value} placeholder="scope" onInput={(e: Event) => scope.value = (e.target as HTMLInputElement).value} />
                  <select style={inputStyle} value={modality.value} onChange={(e: Event) => modality.value = (e.target as HTMLSelectElement).value}>
                    <option value="">all types</option>
                    <option value="fact">fact</option>
                    <option value="episode">episode</option>
                    <option value="reflection">reflection</option>
                    <option value="insight">insight</option>
                    <option value="journal">journal</option>
                  </select>
                  <input style={inputStyle} type="number" min="1" max="30" value={limit.value} onInput={(e: Event) => limit.value = Number((e.target as HTMLInputElement).value || 8)} />
                  <button style={smallButton(true)} disabled={loading.value} onClick={runQA}>{loading.value ? 'Running' : 'Run QA'}</button>
                </div>

                <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap', marginBottom: '12px' }}>
                  <button style={smallButton()} disabled={!!busy.value} onClick={previewInject}>Preview injection</button>
                  <button style={smallButton()} disabled={!!busy.value} onClick={distill}>Distill scope</button>
                  <button style={smallButton()} disabled={!result.value || !props.onSendToAgent} onClick={sendQA}>Send QA to Agent</button>
                </div>

                {prompt && (
                  <div style={{ ...cardStyle, marginBottom: '12px' }}>
                    <div style={{ padding: '8px 10px', borderBottom: '1px solid var(--border-subtle)', background: 'var(--surface)', fontSize: '12px', fontWeight: 700 }}>Injection Preview</div>
                    <pre style={{ margin: 0, padding: '10px', maxHeight: '190px', overflow: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-word', background: 'var(--surface-2)', color: 'var(--text-dim)', fontSize: '11.5px', lineHeight: 1.5 }}>{prompt}</pre>
                  </div>
                )}

                {!rows.length && !loading.value && (
                  <div style={{ padding: '38px 0', textAlign: 'center', color: 'var(--text-dim)', fontSize: '13px' }}>
                    No recalled memories for this query.
                  </div>
                )}

                <div style={{ display: 'grid', gap: '8px' }}>
                  {rows.map((row: any) => MemoryRow(row, true))}
                </div>
              </div>
            )}

            {activeTab.value === 'truth' && (
              <div>
                <div style={{ ...cardStyle, padding: '12px', marginBottom: '12px' }}>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 150px auto', gap: '8px', marginBottom: '8px' }}>
                    <input style={inputStyle} value={truthNewFact.value} placeholder="New fact to check" onInput={(e: Event) => truthNewFact.value = (e.target as HTMLInputElement).value} />
                    <input style={inputStyle} value={truthScope.value} placeholder="scope" onInput={(e: Event) => truthScope.value = (e.target as HTMLInputElement).value} />
                    <button style={smallButton(true)} disabled={!!busy.value} onClick={detectTruth}>Detect</button>
                  </div>
                  {contradictions.value.length > 0 && (
                    <div style={{ display: 'grid', gap: '6px' }}>
                      {contradictions.value.map((c: any) => (
                        <div style={{ fontSize: '12px', color: 'var(--text-dim)' }}>
                          <button style={smallButton()} onClick={() => truthA.value = c.existing_id}>Use {c.existing_id}</button>
                          <span style={{ marginLeft: '8px' }}>{c.explanation}</span>
                        </div>
                      ))}
                    </div>
                  )}
                </div>

                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr auto auto auto', gap: '8px', marginBottom: '12px' }}>
                  <input style={inputStyle} value={truthA.value} placeholder="Fact A id" onInput={(e: Event) => truthA.value = (e.target as HTMLInputElement).value} />
                  <input style={inputStyle} value={truthB.value} placeholder="Fact B id" onInput={(e: Event) => truthB.value = (e.target as HTMLInputElement).value} />
                  <button style={smallButton()} disabled={!!busy.value} onClick={resolveTruth}>Resolve</button>
                  <button style={smallButton(true)} disabled={!!busy.value || !truthResolution.value} onClick={applyTruth}>Apply</button>
                  <button style={smallButton()} disabled={!!busy.value} onClick={resolveAndApply}>Resolve + Apply</button>
                </div>

                {truthResolution.value && (
                  <pre style={{ ...cardStyle, padding: '10px', maxHeight: '160px', overflow: 'auto', whiteSpace: 'pre-wrap', color: 'var(--text-dim)', fontSize: '11.5px' }}>{JSON.stringify(truthResolution.value, null, 2)}</pre>
                )}

                <div style={{ display: 'grid', gap: '8px', marginTop: '12px' }}>
                  {memories.value.slice(0, 10).map(m => MemoryRow(m, true))}
                </div>
              </div>
            )}

            {activeTab.value === 'graph' && (
              <div>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr auto', gap: '8px', marginBottom: '12px' }}>
                  <input style={inputStyle} value={graphCenter.value} placeholder="Memory id" onInput={(e: Event) => graphCenter.value = (e.target as HTMLInputElement).value} />
                  <button style={smallButton(true)} disabled={!!busy.value} onClick={loadGraph}>Load graph</button>
                </div>
                {graph.value ? (
                  <div style={{ display: 'grid', gap: '10px' }}>
                    <div style={cardStyle}>
                      <div style={{ padding: '8px 10px', borderBottom: '1px solid var(--border-subtle)', fontSize: '12px', fontWeight: 700 }}>Edges</div>
                      <div style={{ padding: '10px', display: 'grid', gap: '6px' }}>
                        {(graph.value.edges || []).map((e: any) => (
                          <div style={{ fontSize: '12px', color: 'var(--text-dim)', fontFamily: 'monospace' }}>{e.source_id} -[{e.relation || 'related'}]-{'>'} {e.target_id}</div>
                        ))}
                        {!(graph.value.edges || []).length && <div style={{ color: 'var(--text-dim)', fontSize: '12px' }}>No graph links.</div>}
                      </div>
                    </div>
                    <div style={{ display: 'grid', gap: '8px' }}>
                      {(graph.value.nodes || []).map((n: any) => MemoryRow(n, true))}
                    </div>
                  </div>
                ) : (
                  <div style={{ padding: '38px 0', textAlign: 'center', color: 'var(--text-dim)', fontSize: '13px' }}>
                    Select a memory row or enter an id to inspect 1-hop links.
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      )
    }
  },
})
