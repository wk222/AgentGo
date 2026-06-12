import { defineComponent } from 'vue'
import { PALETTE_GROUPS } from '../workflow/nodeCatalog'

export default defineComponent({
  name: 'WorkflowNodePalette',
  emits: ['add'],
  setup(_, { emit }) {
    return () => (
      <aside class="wf-palette">
        <div class="wf-palette-title">节点库</div>
        <p class="wf-palette-hint">点击添加到画布</p>
        {PALETTE_GROUPS.map(group => (
          <div key={group.category} class="wf-palette-group">
            <div class="wf-palette-group-label">{group.label}</div>
            {group.nodes.map(entry => (
              <button
                key={entry.type}
                type="button"
                class="wf-palette-item"
                onClick={() => emit('add', entry.type)}
              >
                <span class="wf-palette-icon" style={{ background: entry.color }}>
                  {entry.icon || '●'}
                </span>
                <span class="wf-palette-name">{entry.label}</span>
              </button>
            ))}
          </div>
        ))}
      </aside>
    )
  },
})
