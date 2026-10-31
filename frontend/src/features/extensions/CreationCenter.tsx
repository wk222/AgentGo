import { defineComponent, ref, onMounted } from 'vue'
import { Sparkles, Wrench, Workflow, AppWindow, BookOpen, Bot, ArrowUpRight, RefreshCw, History, CheckCircle2, Pin } from 'lucide-vue-next'
import { wailsCall, wailsCallWithTimeout } from '../../shared/wails'
import { sendToAgent, stageState } from '../../stores/agentBridge'
import { pinnedPages, togglePinnedPage, loadGeneratedPages } from '../../stores/generatedPages'
import { useLayout } from '../../stores/useLayout'
import { notify, confirmDialog } from '../../shared/ui/notify'
import './creation-center.css'

const KINDS = [
  {id:'app', label:'应用与页面', icon:AppWindow, hint:'为常用工作生成一个专属界面', example:'做一个装机工单统计面板，可以导入 CSV、按地区汇总并导出结果。', tools:'scaffold_inner_app、update_inner_app_file、verify_inner_app；需要业务执行时连接动态工具或工作流'},
  {id:'tool', label:'工具与连接器', icon:Wrench, hint:'添加计算、文件处理或服务接入能力', example:'创建一个读取 CSV 并按指定列汇总的工具，提供可复用的参数和测试样例。', tools:'create_tool（Python，使用工具提供的参数契约），实际运行样例验证后再报告可用'},
  {id:'workflow', label:'自动化流程', icon:Workflow, hint:'把多步任务变成可复用的流程', example:'创建一个先整理会议记录，再提取待办事项的工作流。', tools:'save_workflow_definition，必须传完整 definition（id/name/nodes/edges），然后用 run_workflow 测试；不得只输出流程描述'},
  {id:'skill', label:'技能与规范', icon:BookOpen, hint:'记住一种做事方法，供后续任务复用', example:'保存一套每周装机支撑报表的处理规范，包含输入格式、汇总口径与核对步骤。', tools:'evolve_skill 保存完整 SKILL.md；检查保存内容并确认技能可被加载'},
  {id:'agent', label:'专用助手', icon:Bot, hint:'为一个领域设置专属职责和工作方式', example:'创建一个代码审查助手，重点检查错误处理、边界条件和回归风险。', tools:'使用 register_inner_app 创建 kind=agent 的 inner app，设置明确的 system_prompt 和能力范围；使用现有内置应用工具保存并实际调用验证'},
] as const

export default defineComponent({
  name:'CreationCenter',
  setup() {
    const layout = useLayout()
    const kind = ref<string>('app')
    const requirement = ref('')
    const data = ref<Record<string, any>>({})
    const loading = ref(false)
    const restoring = ref('')
    const error = ref('')
    const refresh = async () => {
      loading.value = true; error.value = ''
      try { const result = await wailsCall('GetCreationCenter'); if (!result.success) throw Error(result.error || '读取失败'); data.value = result; void loadGeneratedPages() }
      catch (e:any) { error.value = e.message }
      finally { loading.value = false }
    }
    onMounted(refresh)
    const create = () => {
      const text = requirement.value.trim()
      if (!text) return
      const selected = KINDS.find(k => k.id === kind.value)!
      const prompt = [
        '请为 AgentGo 创建一个可复用的扩展。类型：'+selected.label,
        '需求：'+text,
        '先调用 extension_catalog 检查已有能力，能复用时优先复用。实现路径：'+selected.tools+'。',
        '修改已有内容前，先用 manage_extension_versions snapshot 保存相应 tools/workflows/innerapps/skills。完成后实际验证功能，说明测试结果，并保存新版本快照。',
        '界面与业务能力通过动态工具、内置应用、工作流或技能实现；保持 Eino 核心和原生运行外壳稳定。不要把生成代码、结构检查或脚手架完成当作运行测试成功。发现缺少依赖时给出具体原因。',
      ].join('\n\n')
      layout.focusAgent()
      sendToAgent(prompt)
    }
    const restore = async (id:string) => {
      if (restoring.value) return
      const ok = await confirmDialog('将恢复这个版本包含的内容，当前内容会先自动备份。', {title:'恢复扩展版本',okText:'恢复版本'})
      if (!ok) return
      restoring.value = id
      try {
        const result = await wailsCallWithTimeout('ExtensionVersions',120000,'restore',JSON.stringify({id}))
        if (!result.success) throw Error(result.error || '恢复失败')
        notify.success('扩展版本已恢复'); await refresh()
      } catch(e:any) { notify.error(e.message) }
      finally { restoring.value = '' }
    }
    const openApp = (name:string) => { stageState.activeAppName = name; layout.setView('extensions') }
    const catalog = () => [
      ...(data.value.apps || []).map((x:any)=>({...x,type:x.kind==='agent'?'专用助手':'应用',kind:'app'})),
      ...(data.value.tools || []).map((x:any)=>({...x,type:'工具',kind:'tool'})),
      ...(data.value.workflows || []).map((x:any)=>({...x,type:'工作流',kind:'workflow'})),
      ...(data.value.skills || []).map((x:any)=>({...x,type:'技能',kind:'skill'})),
    ]
    return () => {
      const selected = KINDS.find(k => k.id === kind.value)!
      const items = catalog()
      return <section class="creation-center">
        <header class="creation-header"><div><span class="creation-eyebrow"><Sparkles size={14}/> 创建中心</span><h1>让 AgentGo 长出新能力</h1><p>描述你的需要，生成可以保存、复用和继续改进的工具与应用。</p></div><button type="button" class="creation-refresh" onClick={refresh} disabled={loading.value}><RefreshCw size={14}/>{loading.value?'读取中…':'刷新'}</button></header>
        <div class="creation-composer">
          <div class="creation-kind-tabs" role="tablist" aria-label="创建类型">{KINDS.map(k=>{const Icon=k.icon;return <button type="button" key={k.id} role="tab" aria-selected={kind.value===k.id} class={kind.value===k.id?'active':''} onClick={()=>{kind.value=k.id}}><Icon size={17}/><span>{k.label}</span></button>})}</div>
          <div class="creation-prompt-area"><label for="extension-requirement">{selected.hint}</label><textarea id="extension-requirement" value={requirement.value} onInput={(e:Event)=>{requirement.value=(e.target as HTMLTextAreaElement).value}} placeholder={selected.example} rows={4}/><div class="creation-prompt-footer"><span>创建过程会在对话中显示</span><button type="button" class="creation-submit" disabled={!requirement.value.trim()} onClick={create}>开始创建 <ArrowUpRight size={16}/></button></div></div>
        </div>
        {error.value && <div class="creation-error" role="alert">{error.value}<button onClick={refresh}>重试</button></div>}
        <section class="creation-section"><div class="creation-section-heading"><h2>我的扩展与能力 <span>{items.length}</span></h2><button onClick={()=>layout.setView('extensions')}>管理全部扩展 <ArrowUpRight size={13}/></button></div>
          {items.length===0?<div class="creation-empty"><Sparkles size={22}/><strong>{loading.value?'正在读取扩展…':'从你的第一个需求开始'}</strong><p>生成的应用、工具、工作流和技能会集中显示在这里。</p></div>:<div class="creation-inventory">{items.map((item:any)=><article key={item.kind+':'+(item.id||item.name)}><div class="creation-item-top"><span class="creation-type">{item.type}</span>{item.kind==='app' && item.type!=='专用助手' && <button type="button" title={pinnedPages.value.includes(item.name)?'取消固定到导航':'固定到左侧导航'} aria-pressed={pinnedPages.value.includes(item.name)} onClick={()=>togglePinnedPage(item.name)}><Pin size={13}/>{pinnedPages.value.includes(item.name)?'已固定':'固定'}</button>}</div><h3>{item.name}</h3><p>{item.description||'可复用的自定义能力'}</p><button type="button" onClick={()=>item.kind==='app'?openApp(item.name):layout.setView(item.kind==='workflow'?'workflow':'extensions')}>查看 <ArrowUpRight size={13}/></button></article>)}</div>}
        </section>
        <section class="creation-section"><div class="creation-section-heading"><h2><History size={16}/> 版本记录</h2><span>恢复前自动备份</span></div>
          {(data.value.versions||[]).length===0?<div class="creation-versions-empty">暂时没有版本。完成创建后，Agent 会保存扩展快照。</div>:<div class="creation-versions">{data.value.versions.map((v:any)=><div key={v.id}><CheckCircle2 size={16}/><div><strong>{v.title||'扩展快照'}</strong><span>{new Date(v.created_at*1000).toLocaleString()} · {v.items?.length||0} 项</span></div><button type="button" disabled={!!restoring.value} onClick={()=>restore(v.id)}>{restoring.value===v.id?'恢复中…':'恢复'}</button></div>)}</div>}
        </section>
        <p class="creation-boundary">工具、应用页面、专用助手、工作流和技能可以动态加载。原生 Go 程序与 Eino 核心的改动需要重新构建。</p>
      </section>
    }
  },
})
