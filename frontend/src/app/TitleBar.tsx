import { defineComponent, type PropType } from 'vue'
import { Sun, Moon, Settings, Search } from 'lucide-vue-next'
// @ts-ignore
import logoSingle from '../shared/assets/logo_single.png'
import { useLayout } from '../stores/useLayout'
import { stageState } from '../stores/agentBridge'
import WorkspaceSwitcher from './WorkspaceSwitcher'
import type { AgentMode } from '../stores/layoutModel'

const PaneIcon = ({pane}:{pane:number}) => <svg width="17" height="17" viewBox="0 0 24 24" fill="none" aria-hidden="true">
  <rect x="2" y="4" width="20" height="16" rx="2" stroke="currentColor" stroke-width="1.5"/>
  <path d="M8 4v16M16 4v16" stroke="currentColor" stroke-width="1.2"/>
  <rect x={pane===0?3:pane===1?9:17} y="5" width={pane===1?6:4} height="14" fill="currentColor" opacity=".5"/>
</svg>

export default defineComponent({
  name:'AppTitleBar',
  props:{
    sidebarOpen:{type:Boolean,default:true}, agent:{type:String as PropType<AgentMode>,default:'maximized'},
    isDark:{type:Boolean,default:true}, sessionTitle:{type:String,default:''},
    settingsActive:{type:Boolean,default:false}, sending:{type:Boolean,default:false},
    onToggleSidebar:{type:Function as PropType<()=>void>,required:true},
    onSetAgent:{type:Function as PropType<(m:AgentMode)=>void>,required:true},
    onToggleDark:{type:Function as PropType<()=>void>,required:true},
    onOpenSettings:{type:Function as PropType<()=>void>,required:true},
  },
  setup(props){
    const layout=useLayout()
    return ()=>{
      const chatVisible=layout.state.view==='chat' && !layout.state.chatCollapsed && props.agent!=='hidden'
      const workbenchVisible=props.agent!=='maximized'
      return <header class="app-titlebar">
        <div class="titlebar-left">
          <div class="titlebar-brand" title="AgentGo"><img src={logoSingle} class="titlebar-logo" alt=""/><span class="titlebar-brand-name">AgentGo</span></div>
          <div class="titlebar-divider"/><WorkspaceSwitcher/>
        </div>
        <div class="titlebar-center">
          <button type="button" class="titlebar-search-bar" aria-label="搜索文件" title="搜索文件 (Ctrl+P)"
            onClick={()=>{stageState.pendingEditorCommand='file';layout.openWorkbench('code')}}>
            <Search size={14} class="search-icon"/><span class="search-text">搜索文件…</span><span class="search-keys"><kbd>Ctrl P</kbd></span>
          </button>
        </div>
        <div class="titlebar-right">
          <div class="pane-toggle-group" role="group" aria-label="显示或收起三个区域">
            <button type="button" aria-label="左侧导航" aria-expanded={props.sidebarOpen} class={props.sidebarOpen?'active':''}
              title={props.sidebarOpen?'收起左侧导航 (Ctrl+B)':'展开左侧导航 (Ctrl+B)'} onClick={props.onToggleSidebar}><PaneIcon pane={0}/></button>
            <button type="button" aria-label="中间对话" aria-expanded={chatVisible} class={chatVisible?'active':''}
              title={chatVisible?'折叠中间对话':'展开中间对话'} onClick={layout.toggleChat}><PaneIcon pane={1}/></button>
            <button type="button" aria-label="右侧工作台" aria-expanded={workbenchVisible} class={workbenchVisible?'active':''}
              title={workbenchVisible?'收起右侧工作台 (Ctrl+J)':'展开右侧工作台 (Ctrl+J)'} onClick={layout.toggleWorkbench}><PaneIcon pane={2}/></button>
          </div>
          {props.sending && <span class="titlebar-run-indicator" role="status" title="Agent 正在运行"><span class="status-pulse-dot"/></span>}
          <button type="button" class="titlebar-tool-btn" aria-label="切换主题" title="切换主题" onClick={props.onToggleDark}>{props.isDark?<Sun size={13}/>:<Moon size={13}/>}</button>
          <button type="button" class={['titlebar-tool-btn',props.settingsActive&&'active']} aria-label="设置" title="设置" onClick={props.onOpenSettings}><Settings size={13}/></button>
        </div>
      </header>
    }
  },
})
