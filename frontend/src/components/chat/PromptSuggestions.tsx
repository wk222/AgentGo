import { defineComponent, ref, PropType } from 'vue'
import { Sparkles, Brain, Clock, Eye, BookmarkPlus, Search, ChevronDown, ChevronRight } from 'lucide-vue-next'

export interface PromptChipItem {
  icon: any
  label: string
  prompt: string
  color?: string
}

export default defineComponent({
  name: 'PromptSuggestions',
  props: {
    disabled: { type: Boolean, default: false },
    onSelect: { type: Function as PropType<(prompt: string) => void>, required: true },
  },
  setup(props) {
    const expanded = ref(false)
    const CHIPS: PromptChipItem[] = [
      {
        icon: Clock,
        label: '实验监控',
        prompt: '帮我启动后台实验监控任务：执行指定脚本并在后台静默挂载，完成后自动唤醒汇报结果',
      },
      {
        icon: Brain,
        label: '头脑风暴',
        prompt: '调用 brainstorm 工具并发派发 2-3 个发散子分支，分别探索性能优化和边界方案',
      },
      {
        icon: Eye,
        label: '视觉顾问',
        prompt: '调用 vision_advisor 工具分析当前界面设计与组件坐标，检查对齐与视觉层级',
      },
      {
        icon: BookmarkPlus,
        label: '记忆总结',
        prompt: '提炼本轮对话的关键经验与决策，调用 remember 工具存入长效语义记忆库',
      },
      {
        icon: Search,
        label: '经验检索',
        prompt: '在语义记忆库和当前工作区中搜索与该任务相关的历史实现与踩坑经验',
      },
    ]

    return () => (
      <div class="prompt-chips-container">
        <button
          type="button"
          class="prompt-chips-label"
          aria-expanded={expanded.value}
          onClick={() => (expanded.value = !expanded.value)}
        >
          <Sparkles size={12} class="sparkle-icon" />
          <span>常用能力</span>
          <span class="prompt-chips-meta">
            <span class="prompt-chips-count">{CHIPS.length}</span>
            {expanded.value ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
          </span>
        </button>
        {expanded.value && (
          <div class="prompt-chips-list">
            {CHIPS.map((chip, idx) => {
              const Icon = chip.icon
              return (
                <button
                  key={idx}
                  class="prompt-chip-btn"
                  disabled={props.disabled}
                  onClick={() => props.onSelect(chip.prompt)}
                  title={chip.prompt}
                >
                  <Icon size={12} class="chip-icon" />
                  <span>{chip.label}</span>
                </button>
              )
            })}
          </div>
        )}
      </div>
    )
  },
})
