import { defineComponent, ref, PropType } from 'vue'
import {
  PanelLeft,
  LayoutGrid,
  Bot,
  Sun,
  Moon,
  Settings,
  Code2,
  Workflow,
  CheckSquare,
  Cpu,
  Boxes,
  Database,
  Radio,
  Search,
  ChevronDown,
} from 'lucide-vue-next'
// @ts-ignore
import logoSingle from '../logo_single.png'

export default defineComponent({
  name: 'AppTitleBar',
  props: {
    sidebarOpen: { type: Boolean, default: true },
    centerOpen: { type: Boolean, default: true },
    agentOpen: { type: Boolean, default: true },
    isDark: { type: Boolean, default: false },
    projectName: { type: String, default: '默认项目' },
    projectColor: { type: String, default: '#38bdf8' },
    sessionTitle: { type: String, default: 'AgentGo' },
    activeView: { type: String, default: 'code' },
    sending: { type: Boolean, default: false },
    onToggleSidebar: { type: Function as PropType<() => void>, required: true },
    onToggleCenter: { type: Function as PropType<() => void>, required: true },
    onToggleAgent: { type: Function as PropType<() => void>, required: true },
    onToggleDark: { type: Function as PropType<() => void>, required: true },
    onOpenSettings: { type: Function as PropType<() => void>, required: true },
    onSelectView: { type: Function as PropType<(view: string) => void>, required: true },
  },
  setup(props) {
    const isViewMenuOpen = ref(false)

    const VIEWS = [
      { id: 'chat', label: 'Agent 对话', icon: Bot, desc: '对话驱动的任务执行与代码协作' },
      { id: 'code', label: '代码 IDE', icon: Code2, desc: 'Monaco 编辑器与工程资源管理' },
      { id: 'apps', label: '内置应用', icon: Boxes, desc: '微前端与预置多端插件' },
      { id: 'workflow', label: '工作流', icon: Workflow, desc: 'DAG 流程编排与自动化' },
      { id: 'tasks', label: '任务管理', icon: CheckSquare, desc: '长周期任务与计划拆解' },
      { id: 'capabilities', label: '能力中心', icon: Cpu, desc: 'Tool/MCP 注册与执行' },
      { id: 'memory', label: '记忆图谱', icon: Database, desc: '语义长效记忆与知识沉淀' },
      { id: 'channels', label: '网关频道', icon: Radio, desc: '多协议通信与消息分发' },
    ]

    const currentViewObj = () => VIEWS.find((v) => v.id === props.activeView) || VIEWS[0]

    const handleSearchClick = () => {
      // 触发全局 Ctrl+P 事件打开文件搜索
      window.dispatchEvent(
        new KeyboardEvent('keydown', {
          key: 'p',
          code: 'KeyP',
          ctrlKey: true,
          bubbles: true,
        })
      )
    }

    return () => {
      const activeItem = currentViewObj()
      const ActiveIcon = activeItem.icon

      return (
        <header class="app-titlebar">
          {/* Left: Brand & Compact View Selector */}
          <div class="titlebar-left">
            <div class="titlebar-brand" title="AgentGo 桌面智能体开发环境">
              <img src={logoSingle} class="titlebar-logo" alt="AgentGo" />
              <span class="titlebar-brand-name">AgentGo</span>
            </div>

            <div class="titlebar-divider"></div>

            {/* Project Pill */}
            <div class="titlebar-project-pill" title={`当前工作区项目: ${props.projectName}`}>
              <span class="project-dot" style={{ background: props.projectColor || 'var(--accent)' }}></span>
              <span class="project-name">{props.projectName}</span>
            </div>

            {/* Cursor-like Compact View Dropdown Switcher */}
            <div class="titlebar-view-switcher" style={{ position: 'relative' }}>
              <button
                class="titlebar-view-btn"
                onClick={() => (isViewMenuOpen.value = !isViewMenuOpen.value)}
                title="切换工作区工作视图"
              >
                <ActiveIcon size={13} class="view-icon" />
                <span class="view-label">{activeItem.label}</span>
                <ChevronDown size={11} class="view-chevron" />
              </button>

              {isViewMenuOpen.value && (
                <>
                  <div
                    class="titlebar-menu-backdrop"
                    onClick={() => (isViewMenuOpen.value = false)}
                  />
                  <div class="titlebar-view-menu">
                    <div class="view-menu-header">工作区视图</div>
                    {VIEWS.map((v) => {
                      const Icon = v.icon
                      const isActive = props.activeView === v.id
                      return (
                        <div
                          key={v.id}
                          class={['view-menu-item', isActive && 'active']}
                          onClick={() => {
                            props.onSelectView(v.id)
                            isViewMenuOpen.value = false
                          }}
                        >
                          <Icon size={14} class="menu-item-icon" />
                          <div class="menu-item-text">
                            <span class="menu-item-title">{v.label}</span>
                            <span class="menu-item-desc">{v.desc}</span>
                          </div>
                          {isActive && <span class="menu-item-check">✓</span>}
                        </div>
                      )
                    })}
                  </div>
                </>
              )}
            </div>
          </div>

          {/* Center: Cursor Style Global Command Search Bar */}
          <div class="titlebar-center">
            <div
              class="titlebar-search-bar"
              onClick={handleSearchClick}
              title="按 Ctrl+P 快速打开文件，Ctrl+Shift+P 运行 IDE 命令"
            >
              <Search size={13} class="search-icon" />
              <span class="search-text">按 Ctrl+P 快速搜索文件，Ctrl+Shift+P 运行命令…</span>
              <div class="search-keys">
                <kbd>Ctrl P</kbd>
              </div>
            </div>

            {props.sending ? (
              <span class="titlebar-status-badge busy" title="Agent 正在处理与思考">
                <span class="status-pulse-dot"></span>
                <span>处理中</span>
              </span>
            ) : (
              <span class="titlebar-status-badge ready" title="Agent 就绪">
                <span class="status-dot"></span>
                <span>就绪</span>
              </span>
            )}
          </div>

          {/* Right: Layout toggles & Window controls */}
          <div class="titlebar-right">
            {/* Tri-Pane layout toggles */}
            <div class="layout-toggle-group" title="切换界面三栏布局 (侧栏/工作区/Agent)">
              <button
                class={['layout-btn', props.sidebarOpen && 'active']}
                onClick={props.onToggleSidebar}
                title="折叠/展开项目侧边栏 (Ctrl+B)"
              >
                <PanelLeft size={13} />
              </button>
              <button
                class={['layout-btn', props.centerOpen && 'active']}
                onClick={props.onToggleCenter}
                title="折叠/展开主工作区 (IDE / Workflow)"
              >
                <LayoutGrid size={13} />
              </button>
              <button
                class={['layout-btn', props.agentOpen && 'active']}
                onClick={props.onToggleAgent}
                title="折叠/展开 Agent 对话控制台"
              >
                <Bot size={13} />
              </button>
            </div>

            <div class="titlebar-divider"></div>

            {/* Theme switcher */}
            <button
              class="titlebar-tool-btn"
              onClick={props.onToggleDark}
              title={props.isDark ? '切换浅色模式' : '切换深色模式'}
            >
              {props.isDark ? <Sun size={13} /> : <Moon size={13} />}
            </button>

            {/* Settings modal toggle */}
            <button
              class={['titlebar-tool-btn', props.activeView === 'settings' && 'active']}
              onClick={props.onOpenSettings}
              title="全局设置与 LLM 配置"
            >
              <Settings size={13} />
            </button>
          </div>
        </header>
      )
    }
  },
})
