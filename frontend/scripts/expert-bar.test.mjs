import { test } from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { createSSRApp, h, ref, normalizeClass } from 'vue'
import { renderToString } from '@vue/server-renderer'

// 真实的 ExpertBar 组件，接浏览器预览用的 mock 后端：选择、切换、取消、已删除、示例提示。
// 在 Node 里没有 DOM，所以不点真实按钮，而是取组件的 setup 返回的渲染函数，
// 在 VNode 树里找按钮并调用它真正的 onClick，再看树和提示怎么变。

const realSetTimeout = globalThis.setTimeout

function walk(v, out = []) {
  if (v == null || typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean') return out
  if (Array.isArray(v)) { v.forEach(c => walk(c, out)); return out }
  out.push(v)
  walk(v.children, out)
  return out
}
const classOf = v => String(normalizeClass(v?.props?.class) || '')
const hasClass = (v, name) => classOf(v).split(/\s+/).includes(name)
const textOf = v => {
  if (v == null || typeof v === 'boolean') return ''
  if (typeof v === 'string' || typeof v === 'number') return String(v)
  if (Array.isArray(v)) return v.map(textOf).join('')
  return textOf(v.children)
}
const byClass = (tree, name) => walk(tree).filter(v => hasClass(v, name))

async function until(cond, label) {
  for (let i = 0; i < 200; i++) {
    if (cond()) return
    await new Promise(r => realSetTimeout(r, 10))
  }
  assert.fail('等不到：' + label)
}

test('真实组件：专家条的选择、切换、取消、已删除和示例提示', async () => {
  const previous = { window: globalThis.window, document: globalThis.document, localStorage: globalThis.localStorage, setTimeout: globalThis.setTimeout }
  const saved = new Map()
  globalThis.window = { innerWidth: 1200, location: { hostname: 'localhost' } }
  globalThis.document = { documentElement: { classList: { toggle() {} } } }
  globalThis.localStorage = { getItem: k => saved.get(k) ?? null, setItem: (k, v) => saved.set(k, String(v)), removeItem: k => saved.delete(k) }
  // 提示条自己的消失计时器（3~8 秒）不该拖住测试进程。
  globalThis.setTimeout = (fn, ms, ...a) => { const t = realSetTimeout(fn, ms, ...a); if (ms >= 1000) t.unref?.(); return t }
  const server = await createServer({
    root: fileURLToPath(new URL('..', import.meta.url)),
    // ws:false：不开 HMR 的 WebSocket 端口，免得和同时在跑的别的组件测试抢 24678。
    server: { middlewareMode: true, hmr: false, ws: false, watch: null }, appType: 'custom', logLevel: 'silent',
    // 服务端渲染不需要预打包依赖；不扫描，测试结束关服务时也就不会留下一条“Request is outdated”的报错。
    optimizeDeps: { noDiscovery: true, include: [] },
  })
  try {
    const { default: ExpertBar } = await server.ssrLoadModule('/src/features/chat/ExpertBar.tsx')
    const { provideChatContext } = await server.ssrLoadModule('/src/features/chat/chatContext.ts')
    const { mockCall } = await server.ssrLoadModule('/src/shared/ipc/mock/call.ts')
    const { getMockSessions, saveMockSessions } = await server.ssrLoadModule('/src/shared/ipc/mock/state.ts')
    const { uiState } = await server.ssrLoadModule('/src/shared/ui/notify.ts')

    const mount = async (sessionId, messages = []) => {
      const state = { sessionId: ref(sessionId), messages: ref(messages), picked: [], newChats: 0 }
      let render
      const Child = { setup() { render = ExpertBar.setup({ onPickStarter: p => state.picked.push(p) }); return () => render() } }
      const Parent = {
        setup() {
          provideChatContext({
            sessionId: state.sessionId, messages: state.messages, sending: false,
            newChat: async () => { state.newChats++; state.sessionId.value = (await mockCall('NewSession', '新对话')).id },
          })
          return () => h(Child)
        },
      }
      await renderToString(createSSRApp(Parent))
      state.render = () => render()
      return state
    }
    const lastToast = () => uiState.toasts.at(-1)?.text || ''

    // ① 没有专家：折叠，写「未选择」，不列专家
    const sid = (await mockCall('NewSession', '预算')).id
    const bar = await mount(sid)
    await until(() => textOf(bar.render()).includes('未选择'), '未选择')
    assert.equal(byClass(bar.render(), 'expert-bar-body').length, 0, '默认折叠')
    assert.equal(byClass(bar.render(), 'expert-card').length, 0)

    // ② 展开：按分类列出，每位一张卡片；分类顺序是 办公 → 编程 → 研究 → 写作
    byClass(bar.render(), 'expert-bar-head')[0].props.onClick()
    await until(() => byClass(bar.render(), 'expert-card').length === 5, '5 张专家卡片')
    const titles = byClass(bar.render(), 'expert-group-title').map(textOf)
    assert.deepEqual(titles, ['办公', '编程', '研究', '写作'])
    const mine = byClass(bar.render(), 'expert-card').find(c => textOf(c).includes('周报助手'))
    assert.ok(textOf(mine).includes('我的'), '用户自己的专家带「我的」标签')
    assert.ok(textOf(byClass(bar.render(), 'expert-bar-foot')[0]).includes('.md'), '告诉用户怎么写自己的专家')

    // ③ 选一位：标题栏出现名字，后端绑定，提示点明「从下一条消息起生效」不出现（对话还是空的）
    const card = id => byClass(bar.render(), 'expert-card').find(c => c.key === id)
    card('financial-modeler').props.onClick()
    await until(() => textOf(byClass(bar.render(), 'expert-bar-current')[0]).includes('财务建模师'), '当前专家名')
    assert.equal(lastToast(), '已切换为「财务建模师」')
    assert.equal((await mockCall('GetSessionExpert', sid)).expert_id, 'financial-modeler')
    assert.ok(hasClass(card('financial-modeler'), 'active'), '当前专家的卡片高亮')
    assert.equal(hasClass(card('software-engineer'), 'active'), false)

    // ④ 对话还空着时列出示例提示；点一下把原文交给输入框
    const starters = byClass(bar.render(), 'expert-starter-btn')
    assert.equal(starters.length, 2)
    starters[0].props.onClick()
    assert.deepEqual(bar.picked, ['按这份历史数据搭一个三年的收入与现金流预测模型'])

    // ⑤ 对话里已经有用户消息：示例提示收起；这时换专家要说明从下一条起生效
    bar.messages.value = [{ role: 'user', content: '你好' }]
    assert.equal(byClass(bar.render(), 'expert-starter-btn').length, 0)
    card('software-engineer').props.onClick()
    await until(() => textOf(byClass(bar.render(), 'expert-bar-current')[0]).includes('软件工程师'), '换成软件工程师')
    assert.equal(lastToast(), '已切换为「软件工程师」；从下一条消息起生效')

    // ⑥ 取消专家
    byClass(bar.render(), 'expert-clear-btn')[0].props.onClick()
    await until(() => textOf(bar.render()).includes('未选择'), '取消后回到未选择')
    assert.equal(lastToast(), '已取消专家；从下一条消息起生效')
    assert.equal((await mockCall('GetSessionExpert', sid)).expert_id, '')

    // ⑦ 后端拒绝（这条对话在别处被删了）：提示带后端给的原因，标题栏不变
    const doomedId = (await mockCall('NewSession', '马上被删')).id
    const doomed = await mount(doomedId)
    await until(() => textOf(doomed.render()).includes('未选择'), '待删对话渲染')
    byClass(doomed.render(), 'expert-bar-head')[0].props.onClick()
    await until(() => byClass(doomed.render(), 'expert-card').length === 5, '待删对话的专家列表')
    await mockCall('DeleteSession', doomedId)
    byClass(doomed.render(), 'expert-card').find(c => c.key === 'research-analyst').props.onClick()
    await until(() => lastToast() === '这条对话已经不存在', '带后端原因的错误提示')
    assert.ok(textOf(doomed.render()).includes('未选择'), '失败时不能显示成已切换')
    assert.equal(uiState.toasts.at(-1).kind, 'error')

    // ⑧ 对话绑定的专家文件已经没了：照常能用，标题栏说「已删除」并解释
    const sessions = getMockSessions()
    sessions.find(s => s.id === sid).expert_id = 'gone-expert'
    saveMockSessions(sessions)
    const gone = await mount(sid)
    await until(() => byClass(gone.render(), 'expert-bar-current').length === 1, '已删除的专家也要显示')
    assert.equal(textOf(byClass(gone.render(), 'expert-bar-current')[0]), 'gone-expert（已删除）')
    assert.ok(hasClass(byClass(gone.render(), 'expert-bar-current')[0], 'is-missing'))
    assert.ok(textOf(byClass(gone.render(), 'expert-bar-note')[0]).includes('按普通对话工作'))

    // ⑨ 还没有对话（sessionId 为空）时选专家：先新建一条，再绑定到新对话上
    const fresh = await mount('')
    await until(() => textOf(fresh.render()).includes('未选择'), '空会话也能渲染')
    byClass(fresh.render(), 'expert-bar-head')[0].props.onClick()
    await until(() => byClass(fresh.render(), 'expert-card').length === 5, '空会话下也能列出专家')
    byClass(fresh.render(), 'expert-card').find(c => c.key === 'research-analyst').props.onClick()
    await until(() => fresh.newChats === 1 && fresh.sessionId.value !== '', '先新建了对话')
    await until(() => textOf(byClass(fresh.render(), 'expert-bar-current')[0] ?? '').includes('研究分析师'), '新对话绑定了专家')
    assert.equal((await mockCall('GetSessionExpert', fresh.sessionId.value)).expert_id, 'research-analyst')
  } finally {
    await server.close()
    Object.assign(globalThis, previous)
  }
})
