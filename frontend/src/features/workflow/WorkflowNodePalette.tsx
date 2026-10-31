import { defineComponent, ref, computed } from 'vue'
import { Search, ChevronDown, ChevronRight, Plus, PanelLeftClose } from 'lucide-vue-next'
import { PALETTE_GROUPS } from './model/nodeCatalog'

export default defineComponent({
  name: 'WorkflowNodePalette',
  emits: ['add', 'close'],
  setup(_, { emit }) {
    const query = ref('')
    const collapsedGroups = ref<Record<string, boolean>>({})

    const filteredGroups = computed(() => {
      const q = query.value.trim().toLowerCase()
      if (!q) return PALETTE_GROUPS
      return PALETTE_GROUPS.map(g => ({
        ...g,
        nodes: g.nodes.filter(
          n =>
            n.label.toLowerCase().includes(q) ||
            n.type.toLowerCase().includes(q) ||
            n.description.toLowerCase().includes(q),
        ),
      })).filter(g => g.nodes.length > 0)
    })

    const toggleGroup = (cat: string) => {
      collapsedGroups.value[cat] = !collapsedGroups.value[cat]
    }

    const onDragStart = (e: DragEvent, nodeType: string) => {
      if (!e.dataTransfer) return
      e.dataTransfer.setData('application/agentgo-wf-node', nodeType)
      e.dataTransfer.setData('text/plain', nodeType)
      e.dataTransfer.effectAllowed = 'move'
    }

    return () => (
      <aside class="wf-palette coze-palette">
        <div class="coze-palette-head">
          <div class="coze-palette-title-row">
            <span class="coze-palette-title">节点库</span>
            <span class="coze-palette-sub">拖拽或点击添加</span>
          </div>
          <button
            type="button"
            class="coze-palette-close-btn"
            title="收起节点库（可从底部『+ 添加节点』随时展开）"
            onClick={() => emit('close')}
          >
            <PanelLeftClose size={14} />
          </button>
        </div>

        <div class="coze-palette-search-wrap">
          <Search size={13} class="coze-palette-search-icon" />
          <input
            type="text"
            class="coze-palette-search coze-palette-search-input" aria-label="搜索工作流节点"
            value={query.value}
            placeholder="搜索节点 (LLM / HTTP / 循环…)"
            onInput={(e: Event) => {
              query.value = (e.target as HTMLInputElement).value
            }}
          />
          {query.value && (
            <button
              type="button"
              class="coze-palette-search-clear"
              onClick={() => {
                query.value = ''
              }}
            >
              ×
            </button>
          )}
        </div>

        <div class="coze-palette-scroll">
          {filteredGroups.value.map(group => {
            const isCollapsed = !!collapsedGroups.value[group.category] && !query.value.trim()
            return (
              <div key={group.category} class="wf-palette-group coze-palette-group">
                <button
                  type="button"
                  class="coze-palette-group-header" aria-expanded={!isCollapsed}
                  onClick={() => toggleGroup(group.category)}
                >
                  {isCollapsed ? <ChevronRight size={12} /> : <ChevronDown size={12} />}
                  <span class="coze-palette-group-label">{group.label}</span>
                  <span class="coze-palette-group-count">{group.nodes.length}</span>
                </button>

                {!isCollapsed && (
                  <div class="coze-palette-grid">
                    {group.nodes.map(entry => (
                      <button
                        key={entry.type}
                        type="button"
                        draggable={true}
                        class="wf-palette-item coze-palette-card"
                        title={`${entry.label} (${entry.type}) — ${entry.description}\n点击直接添加并连线，或拖拽到画布任意位置`}
                        onDragstart={(e: DragEvent) => onDragStart(e, entry.type)}
                        onClick={() => emit('add', entry.type)}
                      >
                        <span class="wf-palette-icon coze-palette-icon coze-palette-card-icon" style={{ background: entry.color }}>
                          {entry.icon || '✦'}
                        </span>
                        <div class="coze-palette-card-text coze-palette-card-info">
                          <div class="coze-palette-card-top coze-palette-card-title-row">
                            <span class="wf-palette-name coze-palette-card-name">{entry.label}</span>
                            <Plus size={12} class="coze-palette-plus coze-palette-card-plus" />
                          </div>
                          <span class="coze-palette-desc coze-palette-card-desc">{entry.description}</span>
                        </div>
                      </button>
                    ))}
                  </div>
                )}
              </div>
            )
          })}

          {filteredGroups.value.length === 0 && (
            <div class="coze-palette-empty">未找到匹配「{query.value}」的节点</div>
          )}
        </div>
      </aside>
    )
  },
})
