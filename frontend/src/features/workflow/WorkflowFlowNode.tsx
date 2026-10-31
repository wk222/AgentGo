import { defineComponent, computed, type PropType } from 'vue'
import { Handle, Position } from '@vue-flow/core'
import type { WfNodeData } from './model/flowgramAdapter'
import { catalogEntry, normalizeCatalogType } from './model/nodeCatalog'
import { isBranchFlowType, parseBranchHandles } from './model/branchUtils'
import { parseBranchTable } from './model/branchTable'
import { parseSubCanvas } from './model/subCanvas'

function extractTemplateVars(raw?: string): string[] {
  if (!raw) return []
  const matches = Array.from(raw.matchAll(/\{\{\s*([a-zA-Z0-9_.-]+)\s*\}\}/g))
  return Array.from(new Set(matches.map(m => m[1]))).slice(0, 3)
}

function truncateText(s: string | undefined, max = 64): string {
  const clean = String(s || '').trim().replace(/\s+/g, ' ')
  if (!clean) return ''
  return clean.length > max ? `${clean.slice(0, max)}…` : clean
}

export default defineComponent({
  name: 'WorkflowFlowNode',
  props: {
    id: { type: String, required: true },
    data: { type: Object as PropType<WfNodeData>, required: true },
    selected: { type: Boolean, default: false },
  },
  setup(props) {
    const normType = computed(() => normalizeCatalogType(props.data?.flowType || 'LLM'))

    const branches = computed(() => {
      const ft = normType.value
      if (!isBranchFlowType(ft)) return []
      return parseBranchHandles(props.data?.fields)
    })

    const branchDetails = computed(() => {
      const ft = normType.value
      if (!isBranchFlowType(ft)) return new Map<string, string>()
      const rows = parseBranchTable(props.data?.fields?.branch_table)
      const map = new Map<string, string>()
      for (const r of rows) {
        if (r.id && r.when) map.set(r.id, r.when)
      }
      return map
    })

    return () => {
      const d = props.data
      const ft = normType.value
      const entry = catalogEntry(ft)
      const fields = d?.fields || {}
      const isStart = ft === 'Start'
      const isEnd = ft === 'End'
      const isBranch = isBranchFlowType(ft)
      const isContainer = ft === 'Loop' || ft === 'Batch' || ft === 'ForEach'
      const isMerge = ft === 'Merge'
      const subDoc = isContainer ? parseSubCanvas(fields.sub_canvas) : null
      const subNodes = subDoc?.nodes || []
      const runStatus = d?.runStatus || 'idle'

      const renderVarPill = (name: string, typeLabel?: string) => (
        <span class="coze-var-pill">
          <span class="coze-var-brace">{'{x}'}</span>
          <span class="coze-var-name">{name}</span>
          {typeLabel && <em class="coze-var-type">{typeLabel}</em>}
        </span>
      )

      const renderStructuredRows = () => {
        if (isStart) {
          return (
            <div class="coze-node-rows">
              <div class="coze-node-row">
                <span class="coze-row-label">输入参数</span>
                <div class="coze-row-val">{renderVarPill('input', 'String')}</div>
              </div>
              {fields.description && (
                <div class="coze-node-desc-box">{truncateText(fields.description, 52)}</div>
              )}
            </div>
          )
        }

        if (isEnd) {
          const outRef = truncateText(fields.output_var, 28) || 'last'
          return (
            <div class="coze-node-rows">
              <div class="coze-node-row">
                <span class="coze-row-label">返回变量</span>
                <div class="coze-row-val">
                  {renderVarPill(outRef.replace(/^\{\{|\}\}$/g, ''), 'String')}
                </div>
              </div>
            </div>
          )
        }

        if (ft === 'LLM') {
          const refs = extractTemplateVars(fields.prompt)
          const promptText = truncateText(fields.prompt, 72) || '{{input}}'
          return (
            <div class="coze-node-rows">
              <div class="coze-node-row">
                <span class="coze-row-label">模型</span>
                <div class="coze-row-val">
                  <span class="coze-model-pill">✦ Default ChatModel</span>
                </div>
              </div>
              <div class="coze-node-row">
                <span class="coze-row-label">输入</span>
                <div class="coze-row-val">
                  {(refs.length ? refs : ['input']).map(r => renderVarPill(r))}
                </div>
              </div>
              <div class="coze-node-prompt-box">
                <span class="coze-prompt-tag">Prompt</span>
                <span class="coze-prompt-text">{promptText}</span>
              </div>
              <div class="coze-node-row">
                <span class="coze-row-label">输出</span>
                <div class="coze-row-val">{renderVarPill(entry.outputVar || 'output', entry.outputType || 'String')}</div>
              </div>
            </div>
          )
        }

        if (ft === 'Tool') {
          return (
            <div class="coze-node-rows">
              <div class="coze-node-row">
                <span class="coze-row-label">工具</span>
                <div class="coze-row-val">
                  <span class="coze-code-pill">⚙ {fields.tool_name || 'get_current_time'}</span>
                </div>
              </div>
              {fields.args && fields.args !== '{}' && (
                <div class="coze-node-prompt-box">
                  <span class="coze-prompt-tag">Args</span>
                  <span class="coze-prompt-text">{truncateText(fields.args, 56)}</span>
                </div>
              )}
              <div class="coze-node-row">
                <span class="coze-row-label">输出</span>
                <div class="coze-row-val">{renderVarPill('tool_output', 'JSON')}</div>
              </div>
            </div>
          )
        }

        if (ft === 'Agent') {
          return (
            <div class="coze-node-rows">
              <div class="coze-node-row">
                <span class="coze-row-label">智能体</span>
                <div class="coze-row-val">
                  <span class="coze-model-pill">◎ {fields.agent_name || 'assistant'}</span>
                </div>
              </div>
              <div class="coze-node-prompt-box">
                <span class="coze-prompt-tag">Task</span>
                <span class="coze-prompt-text">{truncateText(fields.prompt, 64) || '{{input}}'}</span>
              </div>
              <div class="coze-node-row">
                <span class="coze-row-label">输出</span>
                <div class="coze-row-val">{renderVarPill('agent_reply', 'String')}</div>
              </div>
            </div>
          )
        }

        if (isBranch) {
          return (
            <div class="coze-node-rows coze-branch-rows">
              {branches.value.map((b, idx) => {
                const cond = branchDetails.value.get(b) || (b === 'else' || b === 'default' ? '否则 (默认分支)' : b === 'true' ? '条件成立' : b === 'false' ? '条件不成立' : fields.expression || '条件匹配')
                const isElse = b === 'else' || b === 'default' || b === 'false'
                return (
                  <div key={b} class="coze-branch-row-item">
                    <span class={['coze-branch-tag', isElse && 'is-else']}>
                      {isElse ? 'ELSE' : idx === 0 ? 'IF' : `ELIF ${idx}`}
                    </span>
                    <span class="coze-branch-cond" title={cond}>{truncateText(cond, 28)}</span>
                    <span class="coze-branch-port-name">{b}</span>
                    <Handle
                      type="source"
                      position={Position.Right}
                      id={b}
                      class="wf-handle wf-handle-branch-inline"
                      connectable
                    />
                  </div>
                )
              })}
            </div>
          )
        }

        if (isContainer) {
          const iterSrc = fields.collection || fields.collection_var || '{{last}}'
          const itemVar = fields.item_var || 'item'
          return (
            <div class="coze-node-rows">
              <div class="coze-node-row">
                <span class="coze-row-label">迭代数组</span>
                <div class="coze-row-val">
                  {renderVarPill(iterSrc.replace(/^\{\{|\}\}$/g, ''), 'Array')}
                </div>
              </div>
              <div class="coze-subcanvas-box">
                <div class="coze-subcanvas-head">
                  <span>◫ 内部子画布 ({subNodes.length} 节点)</span>
                  <span class="coze-subcanvas-item-var">当前项: {'{'}{itemVar}{'}'}</span>
                </div>
                <div class="coze-subcanvas-chain">
                  {subNodes.length > 0 ? (
                    subNodes.slice(0, 4).map((sn, i) => (
                      <span key={sn.id} class="coze-subcanvas-chip">
                        {i > 0 && <em class="coze-subcanvas-arrow">→</em>}
                        <span>{normalizeCatalogType(sn.type)}</span>
                      </span>
                    ))
                  ) : (
                    <span class="coze-subcanvas-empty">点击右侧配置面板编辑循环体</span>
                  )}
                </div>
              </div>
              <div class="coze-node-row">
                <span class="coze-row-label">输出</span>
                <div class="coze-row-val">{renderVarPill(entry.outputVar || 'results', 'Array')}</div>
              </div>
            </div>
          )
        }

        // Generic structured rows for HTTP / Bash / Code / Variable / Notify / AskUser / Merge / Parallel / etc.
        const summaryText =
          fields.url
            ? `${fields.method || 'GET'} ${fields.url}`
            : fields.script || fields.code || fields.message || fields.prompt || fields.topic || fields.workflow_id || fields.path || fields.metric || ''

        return (
          <div class="coze-node-rows">
            {ft === 'Variable' && (
              <div class="coze-node-row">
                <span class="coze-row-label">变量赋值</span>
                <div class="coze-row-val">
                  {renderVarPill(fields.name || 'var1', '← ' + truncateText(fields.value || '{{last}}', 16))}
                </div>
              </div>
            )}
            {isMerge && (
              <div class="coze-node-row">
                <span class="coze-row-label">聚合策略</span>
                <div class="coze-row-val">
                  <span class="coze-code-pill">{fields.merge_strategy || 'json_array'} · 多入一出</span>
                </div>
              </div>
            )}
            {summaryText && (
              <div class="coze-node-prompt-box">
                <span class="coze-prompt-tag">{ft}</span>
                <span class="coze-prompt-text">{truncateText(summaryText, 58)}</span>
              </div>
            )}
            <div class="coze-node-row">
              <span class="coze-row-label">输出</span>
              <div class="coze-row-val">
                {renderVarPill(entry.outputVar || 'output', entry.outputType || 'Any')}
              </div>
            </div>
          </div>
        )
      }

      return (
        <div
          class={[
            'wf-flow-node',
            'coze-flow-node',
            isStart && 'coze-flow-node--start',
            isEnd && 'coze-flow-node--end',
            isBranch && 'wf-flow-node--branch',
            isContainer && 'wf-flow-node--container',
            isMerge && 'wf-flow-node--merge',
            props.selected && 'wf-flow-node--selected',
            runStatus !== 'idle' && `is-run-${runStatus}`,
          ]}
          style={{ '--node-accent': entry.color } as any}
        >
          {!isStart && (
            <Handle
              type="target"
              position={Position.Left}
              id="in"
              class="wf-handle wf-handle-in"
              connectable
            />
          )}

          <div class="wf-flow-node-body coze-node-card">
            <div class="coze-node-header">
              <div class="coze-node-icon" style={{ background: entry.color }}>
                {entry.icon || '✦'}
              </div>
              <div class="coze-node-title-col">
                <div class="coze-node-title-line">
                  <span class="coze-node-title">{d?.label || entry.label}</span>
                  {runStatus === 'running' && <span class="coze-node-run-badge running">运行中…</span>}
                  {runStatus === 'success' && <span class="coze-node-run-badge success">✓ 完成</span>}
                  {runStatus === 'error' && <span class="coze-node-run-badge error">✗ 异常</span>}
                </div>
                <span class="coze-node-subtitle">{entry.description}</span>
              </div>
              <span class="coze-node-type-badge">{ft}</span>
            </div>

            {renderStructuredRows()}
          </div>

          {!isEnd && !isBranch && (
            <Handle
              type="source"
              position={Position.Right}
              id="out"
              class="wf-handle wf-handle-out"
              connectable
            />
          )}
        </div>
      )
    }
  },
})
