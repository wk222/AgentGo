import { defineComponent, computed, type PropType } from 'vue'
import { Handle, Position } from '@vue-flow/core'
import type { WfNodeData } from '../workflow/flowgramAdapter'
import { catalogEntry } from '../workflow/nodeCatalog'
import { isBranchFlowType, parseBranchHandles } from '../workflow/branchUtils'
import { parseSubCanvas } from '../workflow/subCanvas'

export default defineComponent({
  name: 'WorkflowFlowNode',
  props: {
    id: { type: String, required: true },
    data: { type: Object as PropType<WfNodeData>, required: true },
    selected: { type: Boolean, default: false },
  },
  setup(props) {
    const branches = computed(() => {
      const ft = props.data?.flowType || 'LLM'
      if (!isBranchFlowType(ft)) return []
      return parseBranchHandles(props.data?.fields)
    })

    return () => {
      const d = props.data
      const ft = d?.flowType || 'LLM'
      const entry = catalogEntry(ft)
      const isStart = ft === 'Start'
      const isEnd = ft === 'End'
      const isBranch = isBranchFlowType(ft)
      const isContainer = ft === 'Loop' || ft === 'Batch' || ft === 'ForEach'
      const isMerge = ft === 'Merge'
      const subCount = isContainer ? parseSubCanvas(d?.fields?.sub_canvas).nodes.length : 0
      const preview = d?.fields?.prompt || d?.fields?.tool_name || d?.fields?.url || d?.fields?.message || d?.fields?.expression || d?.fields?.collection || ''

      return (
        <div class={['wf-flow-node', isBranch && 'wf-flow-node--branch', isContainer && 'wf-flow-node--container', isMerge && 'wf-flow-node--merge', props.selected && 'wf-flow-node--selected']}>
          {!isStart && (
            <Handle
              type="target"
              position={Position.Left}
              id="in"
              class="wf-handle wf-handle-in"
              connectable
            />
          )}
          <div class="wf-flow-node-body" style={{ borderColor: entry.color }}>
            <div class="wf-flow-node-icon" style={{ background: entry.color }}>
              {entry.icon || '●'}
            </div>
            <div class="wf-flow-node-text">
              <div class="wf-flow-node-label">{d?.label || props.id}</div>
              <div class="wf-flow-node-type">{ft}</div>
              {isMerge ? (
                <div class="wf-flow-node-sub">Parallel 汇合 · 多入一出</div>
              ) : isContainer ? (
                <div class="wf-flow-node-sub">子画布 · {subCount} 节点</div>
              ) : preview ? (
                <div class="wf-flow-node-preview">{preview.slice(0, 48)}{preview.length > 48 ? '…' : ''}</div>
              ) : null}
            </div>
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
          {!isEnd && isBranch && branches.value.map((b, i) => {
            const n = branches.value.length
            const topPct = ((i + 1) / (n + 1)) * 100
            return (
              <div key={b} class="wf-branch-outlet" style={{ top: `${topPct}%` }}>
                <span class="wf-branch-outlet-label">{b}</span>
                <Handle
                  type="source"
                  position={Position.Right}
                  id={b}
                  class="wf-handle wf-handle-branch"
                  connectable
                  style={{ top: '50%', right: '-6px', transform: 'translateY(-50%)' }}
                />
              </div>
            )
          })}
        </div>
      )
    }
  },
})
