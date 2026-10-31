import { test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { createSSRApp, h, nextTick } from 'vue'
import { renderToString } from '@vue/server-renderer'

test('真实组件渲染：标题栏中间搜索，右侧三个开关，类型切换属于工作台', async () => {
  const previous = {window:globalThis.window,document:globalThis.document,localStorage:globalThis.localStorage}
  const saved=new Map()
  globalThis.window={innerWidth:1200,location:{hostname:'localhost'}}
  globalThis.document={documentElement:{classList:{toggle(){}}}}
  globalThis.localStorage={getItem:k=>saved.get(k)??null,setItem:(k,v)=>saved.set(k,String(v)),removeItem:k=>saved.delete(k)}
  const server=await createServer({
    root:fileURLToPath(new URL('..',import.meta.url)),
    server:{middlewareMode:true,hmr:false,watch:null},appType:'custom',
  })
  try {
    const {default:TitleBar}=await server.ssrLoadModule('/src/app/TitleBar.tsx')
    const {default:WorkbenchTabs}=await server.ssrLoadModule('/src/app/WorkbenchTabs.tsx')
    const {useLayout}=await server.ssrLoadModule('/src/stores/useLayout.ts')
    const {stageState}=await server.ssrLoadModule('/src/stores/agentBridge.ts')
    const layout=useLayout()
    const props={
      get sidebarOpen(){return layout.state.sidebarOpen},
      get agent(){return layout.state.agent},
      isDark:true,settingsActive:false,sending:false,
      onToggleSidebar:layout.toggleSidebar,onSetAgent:layout.setAgent,
      onToggleDark:layout.toggleTheme,onOpenSettings:()=>{},
    }
    const render=()=>h(TitleBar,props)
    const html=await renderToString(createSSRApp({render}))
    const center=html.slice(html.indexOf('class="titlebar-center"'),html.indexOf('class="titlebar-right"'))
    assert.ok(center.includes('aria-label="搜索文件"'))
    assert.equal(html.includes('aria-label="工作台类型"'),false)
    assert.equal((html.match(/aria-label="(?:左侧导航|中间对话|右侧工作台)"/g)||[]).length,3)
    let titleRender
    await renderToString(createSSRApp({setup(){titleRender=TitleBar.setup(props,{});return ()=>titleRender()}}))
    const find=(v,label)=>{
      if(!v||typeof v!=='object')return null
      if(v.props?.['aria-label']===label)return v
      for(const child of Array.isArray(v)?v:Array.isArray(v.children)?v.children:[]){const result=find(child,label);if(result)return result}
      return null
    }
    // Invoke the real buttons, not a second implementation of their behavior.
    find(titleRender(),'右侧工作台').props.onClick()
    assert.equal(layout.state.agent,'docked')
    const paneHtml=await renderToString(createSSRApp({render:()=>h(WorkbenchTabs)}))
    for(const label of ['代码','记忆','工作流','应用'])assert.ok(paneHtml.includes('切换到'+label+'工作台'))
    let typeRender
    await renderToString(createSSRApp({setup(){typeRender=WorkbenchTabs.setup();return ()=>typeRender()}}))
    find(typeRender(),'切换到工作流工作台').props.onClick()
    assert.equal(layout.state.workbenchType,'workflow')
    find(titleRender(),'中间对话').props.onClick()
    assert.equal(layout.state.chatCollapsed,true)
    find(titleRender(),'右侧工作台').props.onClick()
    assert.equal(layout.state.agent,'maximized')
    assert.equal(layout.state.chatCollapsed,true)
    find(titleRender(),'中间对话').props.onClick()
    assert.equal(layout.state.chatCollapsed,false)
    const sidebar=layout.state.sidebarOpen
    find(titleRender(),'左侧导航').props.onClick()
    assert.equal(layout.state.sidebarOpen,!sidebar)
    find(titleRender(),'搜索文件').props.onClick()
    assert.equal(layout.state.agent,'docked')
    assert.equal(layout.state.workbenchType,'code')
    assert.equal(stageState.pendingEditorCommand,'file')
    await nextTick()
  } finally {
    await server.close()
    Object.assign(globalThis,previous)
  }
})
