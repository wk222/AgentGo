// 布局模型单测：与 runtime-client 测试同样的做法，不引入额外测试依赖。
const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')

const file = path.resolve(__dirname, '../src/stores/layoutModel.ts')
const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText
const mod = new Module(file)
mod._compile(out, file)
const m = mod.exports

function mem(init = {}) {
  const data = { ...init }
  return {
    data,
    getItem: k => (k in data ? data[k] : null),
    setItem: (k, v) => { data[k] = String(v) },
    removeItem: k => { delete data[k] },
  }
}

test('空存储：默认值，宽屏默认展开侧栏，并写入新键', () => {
  const kv = mem()
  const s = m.loadLayout(kv, 1600)
  assert.equal(s.sidebarOpen, true)
  assert.equal(s.agent, 'maximized')
  assert.equal(s.view, 'chat')
  assert.ok(kv.data[m.LAYOUT_KEY])
  assert.equal(m.loadLayout(mem(), 1200).sidebarOpen, true)
  assert.equal(m.loadLayout(mem(), 700).sidebarOpen, false)
})

test('旧键迁移：只开 agent = maximized；迁移后旧键被删除', () => {
  const kv = mem({
    'agentgo-dark': 'false', 'agentgo-center-open': 'false', 'agentgo-agent-open': 'true',
    'agentgo-sidebar-open': 'true', 'agentgo-sidebar-width': '300', 'agentgo-rightpanel-width': '540',
    'agentgo-workspace-panel': 'open', 'agentgo-projects': '[{"id":"x"}]',
  })
  const s = m.loadLayout(kv, 1200)
  assert.equal(s.theme, 'light')
  assert.equal(s.agent, 'maximized')
  assert.equal(s.sidebarOpen, true)
  assert.equal(s.sidebarWidth, 300)
  assert.equal(s.agentWidth, 540)
  assert.equal(s.contextOpen, true)
  for (const k of Object.keys(kv.data)) assert.ok(!k.startsWith('agentgo-'), `旧键 ${k} 未删除`)
})

test('旧键迁移：agent 关闭 = hidden；两者都开 = docked；宽度被夹取', () => {
  assert.equal(m.loadLayout(mem({ 'agentgo-agent-open': 'false' }), 1200).agent, 'hidden')
  assert.equal(m.loadLayout(mem({ 'agentgo-center-open': 'true', 'agentgo-agent-open': 'true' }), 1200).agent, 'docked')
  assert.equal(m.loadLayout(mem({ 'agentgo-sidebar-width': '9999' }), 1200).sidebarWidth, m.LAYOUT_LIMITS.sidebarMax)
})

test('新键损坏或字段非法：回退默认，不抛错，不动旧键', () => {
  const bad = mem({ [m.LAYOUT_KEY]: '{not json', 'agentgo-dark': 'false' })
  assert.doesNotThrow(() => m.loadLayout(bad, 1200))
  const s = m.normalizeLayout({ agent: 'weird', sidebarWidth: 'x', view: 42, theme: 'neon' }, 1200)
  assert.equal(s.agent, 'maximized')
  assert.equal(s.sidebarWidth, 240)
  assert.equal(s.view, 'chat')
  assert.equal(s.theme, 'dark')
  assert.equal(m.normalizeLayout({ contextTab: 'xray' }, 1200).contextTab, 'xray')
})

test('存储抛错时仍返回可用状态', () => {
  const kv = { getItem() { throw new Error('denied') }, setItem() { throw new Error('denied') }, removeItem() {} }
  assert.equal(m.loadLayout(kv, 1200).view, 'chat')
})

test('snapToNearest / clamp', () => {
  assert.equal(m.snapToNearest(236, m.LAYOUT_LIMITS.sidebarSnap), 240)
  assert.equal(m.snapToNearest(270, m.LAYOUT_LIMITS.sidebarSnap), 270)
  assert.equal(m.clamp(5, 10, 20), 10)
})

 test('旧布局只迁移一次：保留主题和宽度，进入对话；新版偏好继续保留', () => {
 const kv=mem({[m.LAYOUT_KEY]:JSON.stringify({version:1,theme:'light',agent:'docked',view:'workflow',sidebarWidth:300})})
 const migrated=m.loadLayout(kv,1280)
 assert.equal(migrated.agent,'maximized');assert.equal(migrated.theme,'light');assert.equal(migrated.sidebarWidth,300)
 m.saveLayout(kv,{...migrated,agent:'hidden'})
 assert.equal(m.loadLayout(kv,1280).agent,'hidden')
 })

function loadReactiveLayout(storage = mem()) {
  const layoutFile = path.resolve(__dirname, '../src/stores/useLayout.ts')
  const previous = { window: global.window, document: global.document, localStorage: global.localStorage }
  global.window = { innerWidth: 1440 }
  global.document = { createElement: () => ({}), documentElement: { classList: { toggle() {} } } }
  global.localStorage = storage
  const layoutMod = new Module(layoutFile)
  layoutMod.filename = layoutFile
  layoutMod.paths = Module._nodeModulePaths(path.dirname(layoutFile))
  const baseRequire = layoutMod.require.bind(layoutMod)
  layoutMod.require = id => id === './layoutModel' ? m : baseRequire(id)
  const vue = baseRequire('vue')
  const scope = vue.effectScope()
  scope.run(() => layoutMod._compile(ts.transpileModule(fs.readFileSync(layoutFile, 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText, layoutFile))
  return { layout: layoutMod.exports.useLayout(), close() { scope.stop(); Object.assign(global, previous) } }
}

test('切页面、关闭设置、切会话均不改变工作台状态；代码链接只打开文件面板', () => {
  const env = loadReactiveLayout()
  const l = env.layout
  try {
    for (const mode of ['maximized', 'docked', 'hidden']) {
      l.setAgent(mode)
      for (const page of ['tasks', 'create', 'extensions', 'app:demo']) {
        l.setView(page)
        assert.equal(l.state.view, page)
        assert.equal(l.state.agent, mode)
      }
      l.setView('settings'); l.closeSettings()
      assert.equal(l.state.view, 'app:demo')
      assert.equal(l.state.agent, mode)
    }
    l.setView('tasks'); l.closeWorkbench(); l.setView('code')
    assert.equal(l.state.view, 'tasks')
    assert.equal(l.state.agent, 'docked')
    assert.equal(l.sceneTabs.openTabs.includes('code'), false)
    l.focusAgent()
    assert.equal(l.state.view, 'chat')
    assert.equal(l.state.agent, 'docked')
    l.closeWorkbench()
    assert.equal(l.state.view, 'chat')
    l.openWorkbench()
    assert.equal(l.state.view, 'chat')
  } finally { env.close() }
})

test('旧代码主页迁移为对话；保留主题、宽度和其他主页面选择', () => {
  const kv = mem({ [m.LAYOUT_KEY]: JSON.stringify({version:2,view:'code',agent:'docked',theme:'light',sidebarWidth:300}) })
  assert.equal(m.loadLayout(kv,1440).view,'chat')
  assert.equal(m.loadLayout(kv,1440).theme,'light')
  assert.equal(m.loadLayout(kv,1440).sidebarWidth,300)
  const env = loadReactiveLayout()
  try {
    env.layout.setView('workflow')
    env.layout.openWorkbench()
    env.layout.closeSceneTab('workflow')
    assert.equal(env.layout.state.view,'chat')
    assert.equal(env.layout.state.agent,'docked')
  } finally { env.close() }
})

test('工作台类型切换保留主页面；折叠对话可恢复，关闭工作台不会留下空白主区', () => {
 const env=loadReactiveLayout(),l=env.layout
 try {
  l.setView('create')
  for(const type of ['code','memory','workflow','apps']) {
   l.openWorkbench(type)
   assert.equal(l.state.view,'create')
   assert.equal(l.state.workbenchType,type)
  }
  l.setView('memory')
  assert.equal(l.state.view,'create')
  assert.equal(l.state.workbenchType,'memory')
  l.setAgent('hidden'); l.openWorkbench('workflow')
  assert.equal(l.state.agent,'hidden')
  l.focusAgent();l.toggleChat()
  assert.equal(l.state.chatCollapsed,true)
  assert.notEqual(l.state.agent,'maximized')
  l.closeWorkbench()
  assert.equal(l.state.chatCollapsed,true)
  l.toggleChat();l.revealAgent()
  assert.equal(l.state.chatCollapsed,false)
  assert.equal(l.state.workbenchType,'workflow')
 } finally {env.close()}
})

test('对象引用仅携带有界定位数据和版本，不自动传输原文', () => {
 const source=path.resolve(__dirname,'../src/stores/workbenchContext.ts')
 const module=new Module(source)
 module._compile(ts.transpileModule(fs.readFileSync(source,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2020}}).outputText,source)
 const items=Array.from({length:50},(_,i)=>({kind:'memory',id:String(i),label:'a'.repeat(10000),scope:'session-x',version:'v1',content:'NEVER_INLINE_THIS'}))
 const prompt=module.exports.objectReferencesPrompt(items)
 assert.equal((prompt.match(/"kind"/g)||[]).length,8)
 assert.equal(prompt.includes('NEVER_INLINE_THIS'),false)
 assert.ok(prompt.length<5000)
 assert.ok(prompt.includes('"scope":"session-x"'))
 assert.ok(prompt.includes('"version":"v1"'))
 assert.equal(module.exports.objectReferencesPrompt([]),'')
})

test('窄窗口折叠中间对话会真正隐藏；左右折叠不代替中间状态', () => {
 const env=loadReactiveLayout(),l=env.layout
 try {
  for (const width of [600,900,1200,1440]) {
   l.focusAgent();l.closeWorkbench()
   const sidebar=l.state.sidebarOpen
   l.toggleChat()
   assert.equal(l.state.chatCollapsed,true)
   assert.equal(l.state.agent,'maximized')
   assert.equal(l.state.sidebarOpen,sidebar)
   assert.deepEqual(m.paneVisibility(l.state,width),{main:false,workbench:false,overlay:false,rail:true,empty:true})
   l.openWorkbench('workflow')
   const folded=m.paneVisibility(l.state,width)
   assert.equal(folded.main,false);assert.equal(folded.overlay,false);assert.equal(folded.workbench,true)
   l.toggleSidebar()
   assert.equal(l.state.chatCollapsed,true)
   l.closeWorkbench()
   assert.equal(l.state.chatCollapsed,true)
   l.toggleChat()
   assert.equal(l.state.chatCollapsed,false)
   assert.equal(l.state.agent,'maximized')
   assert.equal(m.paneVisibility(l.state,width).main,true)
  }
  const restored=m.normalizeLayout({...l.state,chatCollapsed:true,agent:'maximized'},900)
  assert.equal(restored.chatCollapsed,true)
 }finally{env.close()}
})
