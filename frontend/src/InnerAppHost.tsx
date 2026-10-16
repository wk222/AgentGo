import { defineComponent, onMounted, onUnmounted, ref, watch, PropType } from 'vue'
import { wailsCall } from './wails'

const escapeScript = (s: string) => s.replace(/<\/script/gi, '<\\/script')

const helperScript = (name: string, sessionId: string, nonce: string, manifest: any) => `<script>
(function () {
  const appName = ${JSON.stringify(name)};
  const sessionId = ${JSON.stringify(sessionId)};
  const nonce = ${JSON.stringify(nonce)};
  const manifest = ${escapeScript(JSON.stringify(manifest || {}))};
  const actionSet = new Set((manifest.actions || []).map(function (a) { return String(a.name || a); }));
  const openPolicy = manifest.export_policy === 'open';
  let seq = 0;
  const pending = new Map();
  window.addEventListener('error', function (ev) {
    window.parent.postMessage({
      type: 'agentgo:inner-app-runtime-error',
      app: appName,
      session_id: sessionId,
      error: (ev && ev.message) ? String(ev.message) : 'Unknown runtime error'
    }, '*');
  });
  window.addEventListener('unhandledrejection', function (ev) {
    window.parent.postMessage({
      type: 'agentgo:inner-app-runtime-error',
      app: appName,
      session_id: sessionId,
      error: (ev && ev.reason) ? String(ev.reason.message || ev.reason) : 'Unhandled promise rejection'
    }, '*');
  });
  window.addEventListener('message', function (ev) {
    const msg = ev.data || {};
    if (msg.type !== 'agentgo:inner-app-api:result') return;
    if (msg.session_id && msg.session_id !== sessionId) return;
    const p = pending.get(msg.id);
    if (!p) return;
    pending.delete(msg.id);
    if (msg.ok) p.resolve(msg.result);
    else p.reject(new Error(msg.error || 'inner app call failed'));
  });
  window.agentGo = {
    appName: appName,
    sessionId: sessionId,
    manifest: manifest,
    actions: manifest.actions || [],
    canCall: function (action) {
      action = String(action || '');
      return openPolicy || action === 'info' || actionSet.has(action);
    },
    apiCall: function (action, payload) {
      action = String(action || '');
      if (!this.canCall(action)) {
        return Promise.reject(new Error('inner app action is not exported: ' + action));
      }
      const id = 'call_' + (++seq) + '_' + Date.now();
      window.parent.postMessage({
        type: 'agentgo:inner-app-api',
        id,
        app: appName,
        session_id: sessionId,
        nonce: nonce,
        action: action,
        payload: payload || {}
      }, '*');
      return new Promise(function (resolve, reject) {
        pending.set(id, { resolve, reject });
        setTimeout(function () {
          if (!pending.has(id)) return;
          pending.delete(id);
          reject(new Error('inner app call timeout'));
        }, 15000);
      });
    },
    chat: function (input) { return this.apiCall('chat', { input }); }
  };
  window.parent.postMessage({ type: 'agentgo:inner-app-ready', app: appName, session_id: sessionId }, '*');
})();
</script>`

interface AppHtmlSnapshot {
  version: number
  time: string
  html: string
}

export default defineComponent({
  name: 'InnerAppHost',
  props: {
    appName: { type: String, required: true },
    title: { type: String, default: '' },
    onSendToAgent: { type: Function as PropType<(text: string) => void>, default: undefined },
  },
  setup(props) {
    const html = ref('')
    const loading = ref(false)
    const error = ref('')
    const frameRef = ref<HTMLIFrameElement | null>(null)
    const session = ref<any | null>(null)
    const manifest = ref<any | null>(null)

    // 握手与快照回滚状态
    const handshakeState = ref<'pending' | 'ready' | 'runtime_error'>('pending')
    const runtimeErrors = ref<string[]>([])
    const snapshots = ref<AppHtmlSnapshot[]>([])
    const activeSnapshotIdx = ref(-1)

    // 伴随小窗状态
    const companionOpen = ref(false)
    const companionPrompt = ref('')
    const evolving = ref(false)
    const evolveStatus = ref('')

    const readAssetText = async (name: string, rel: string) => {
      const r = await wailsCall('ReadInnerAppFile', name, rel)
      if (r?.success === false) throw new Error(r?.error || `读取 ${rel} 失败`)
      if (r?.text != null) return String(r.text)
      if (r?.base64 && r?.mime) return `data:${r.mime};base64,${r.base64}`
      return ''
    }

    const prepareHTML = async (name: string, raw: string, hostSession: any) => {
      let out = raw || ''
      const helpers = helperScript(name, String(hostSession?.session_id || ''), String(hostSession?.nonce || ''), hostSession?.manifest || {})
      out = out.replace(/<script\b[^>]*agentgo-app-helpers[^>]*><\/script>/gi, helpers)
      if (!/agentGo\s*=/.test(out) && !/agentgo-app-helpers/i.test(out)) {
        out = out.replace(/<\/head>/i, helpers + '</head>')
      }

      const links = Array.from(out.matchAll(/<link\b([^>]*?)href=["']([^"']+\.css)["']([^>]*)>/gi))
      for (const m of links) {
        const href = m[2]
        if (/^(https?:|data:|blob:)/i.test(href)) continue
        try {
          const css = await readAssetText(name, href.replace(/^\//, ''))
          out = out.replace(m[0], `<style data-inline-from="${href}">\n${escapeScript(css)}\n</style>`)
        } catch {}
      }

      const scripts = Array.from(out.matchAll(/<script\b([^>]*?)src=["']([^"']+\.js)["']([^>]*)><\/script>/gi))
      for (const m of scripts) {
        const src = m[2]
        if (/agentgo-app-helpers/i.test(src) || /^(https?:|data:|blob:)/i.test(src)) continue
        try {
          const js = await readAssetText(name, src.replace(/^\//, ''))
          out = out.replace(m[0], `<script data-inline-from="${src}">\n${escapeScript(js)}\n</script>`)
        } catch {}
      }
      return out
    }

    const recordSnapshot = (preparedHtml: string) => {
      if (!preparedHtml) return
      const last = snapshots.value[snapshots.value.length - 1]
      if (last && last.html === preparedHtml) {
        activeSnapshotIdx.value = snapshots.value.length - 1
        return
      }
      const nextVer = snapshots.value.length + 1
      const nowStr = new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
      snapshots.value.push({ version: nextVer, time: nowStr, html: preparedHtml })
      activeSnapshotIdx.value = snapshots.value.length - 1
    }

    const rollbackSnapshot = () => {
      if (activeSnapshotIdx.value <= 0) return
      activeSnapshotIdx.value -= 1
      const target = snapshots.value[activeSnapshotIdx.value]
      if (target) {
        runtimeErrors.value = []
        handshakeState.value = 'pending'
        html.value = target.html
        evolveStatus.value = `已回滚至快照 v${target.version} (${target.time})`
      }
    }

    const load = async () => {
      const name = props.appName.trim()
      if (!name) return
      loading.value = true
      error.value = ''
      html.value = ''
      session.value = null
      manifest.value = null
      handshakeState.value = 'pending'
      runtimeErrors.value = []
      try {
        const hostSession = await wailsCall('OpenInnerAppSession', name)
        if (hostSession?.success === false) {
          error.value = hostSession?.error || 'Open Inner App session failed'
          return
        }
        session.value = hostSession
        manifest.value = hostSession?.manifest || null
        const r = await wailsCall('GetInnerAppPageHTML', name)
        if (r?.success) {
          const prepared = await prepareHTML(name, r.html || '', hostSession)
          html.value = prepared
          recordSnapshot(prepared)
        } else {
          error.value = r?.error || '打开 Inner App UI 失败'
        }
      } catch (e: any) {
        error.value = e?.message || '打开 Inner App UI 失败'
      } finally {
        loading.value = false
      }
    }

    const evolveInPlace = async (instructionText?: string) => {
      const prompt = (instructionText ?? companionPrompt.value).trim()
      const name = props.appName.trim()
      if (!prompt || !name || evolving.value) return
      evolving.value = true
      evolveStatus.value = '正在热演进生成新版本 Bundle…'
      try {
        const errContext = runtimeErrors.value.length
          ? `\n当前运行时异常需一并修复：${runtimeErrors.value.join('; ')}`
          : ''
        const r = await wailsCall(
          'BuildInnerAppIteratively',
          name,
          props.title || name,
          `在保留现有核心功能的基础上迭代修改 Inner App (${name})：${prompt}${errContext}`,
          'static',
          '',
          '',
          2,
          true,
        )
        if (r?.success) {
          companionPrompt.value = ''
          await load()
          evolveStatus.value = `热更新完成 (v${snapshots.value.length})，若效果不佳可一键回滚`
        } else {
          evolveStatus.value = r?.error || '热演进失败，已保持原版本'
        }
      } catch (e: any) {
        evolveStatus.value = e?.message || '热演进失败'
      } finally {
        evolving.value = false
      }
    }

    const sendCompanionToMainChat = () => {
      const prompt = companionPrompt.value.trim()
      const name = props.appName.trim()
      if (!prompt || !props.onSendToAgent) return
      const errBlock = runtimeErrors.value.length
        ? `\n当前检测到运行时报错：\`${runtimeErrors.value.join(' | ')}\``
        : ''
      props.onSendToAgent(
        `[Creative Mode · InnerApp 演进] 请针对内置应用 \`${name}\` 进行迭代修改：\n${prompt}${errBlock}`,
      )
      companionPrompt.value = ''
      evolveStatus.value = '已发送至右侧 Agent 控制台'
    }

    const handleInnerAppMessage = async (ev: MessageEvent) => {
      const msg: any = ev.data || {}
      const source = ev.source as Window | null
      if (!source) return
      if (frameRef.value?.contentWindow && source !== frameRef.value.contentWindow) return

      if (msg.type === 'agentgo:inner-app-ready') {
        if (handshakeState.value !== 'runtime_error') {
          handshakeState.value = 'ready'
        }
        return
      }

      if (msg.type === 'agentgo:inner-app-runtime-error') {
        handshakeState.value = 'runtime_error'
        const errText = String(msg.error || 'Runtime error')
        if (!runtimeErrors.value.includes(errText)) {
          runtimeErrors.value.push(errText)
        }
        return
      }

      if (msg.type !== 'agentgo:inner-app-api') return
      const name = String(msg.app || props.appName || '').trim()
      const currentSession = session.value
      try {
        if (!currentSession?.session_id || !currentSession?.nonce) throw new Error('inner app session not ready')
        if (msg.session_id !== currentSession.session_id || msg.nonce !== currentSession.nonce) throw new Error('inner app session mismatch')
        const action = String(msg.action || '')
        const mf = manifest.value || {}
        const actionNames = new Set((mf.actions || []).map((a: any) => String(a.name || a)))
        if (mf.export_policy !== 'open' && action !== 'info' && !actionNames.has(action)) {
          throw new Error(`inner app action is not exported: ${action}`)
        }
        const r = await wailsCall('CallInnerAppSessionAPI', name, currentSession.session_id, currentSession.nonce, action, JSON.stringify(msg.payload || {}))
        source.postMessage({ type: 'agentgo:inner-app-api:result', id: msg.id, ok: r?.success !== false, result: r, session_id: currentSession.session_id }, '*')
      } catch (e: any) {
        source.postMessage({ type: 'agentgo:inner-app-api:result', id: msg.id, ok: false, error: e?.message || '调用失败' }, '*')
      }
    }

    onMounted(() => {
      window.addEventListener('message', handleInnerAppMessage)
      void load()
    })
    onUnmounted(() => window.removeEventListener('message', handleInnerAppMessage))
    watch(() => props.appName, () => {
      snapshots.value = []
      activeSnapshotIdx.value = -1
      void load()
    })

    return () => {
      const currentSnap = snapshots.value[activeSnapshotIdx.value]
      const canRollback = activeSnapshotIdx.value > 0

      return (
        <div class="inner-app-host" style={{ position: 'relative' }}>
          <div class="inner-app-manifest-bar" style={{ justifyContent: 'space-between' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', flexWrap: 'wrap' }}>
              <span
                class={[
                  'inner-app-handshake-badge',
                  handshakeState.value === 'ready' && 'ok',
                  handshakeState.value === 'runtime_error' && 'err',
                ]}
                title="Inner App 宿主握手与运行时状态监控"
              >
                {handshakeState.value === 'ready'
                  ? `● Ready${currentSnap ? ` · v${currentSnap.version}` : ''}`
                  : handshakeState.value === 'runtime_error'
                    ? `⚠ 运行时异常 (${runtimeErrors.value.length})`
                    : '◌ 握手中…'}
              </span>
              {manifest.value && (
                <>
                  <span>{manifest.value.export_policy === 'open' ? 'open policy' : 'exports'}</span>
                  {(manifest.value.actions || []).slice(0, 6).map((a: any) => (
                    <code key={String(a.name || a)}>{String(a.name || a)}</code>
                  ))}
                </>
              )}
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
              {canRollback && (
                <button
                  type="button"
                  class="inner-app-bar-btn warn"
                  onClick={rollbackSnapshot}
                  title="回滚到上一个可用快照版本"
                >
                  ↶ 回滚上一版 (v{snapshots.value[activeSnapshotIdx.value - 1]?.version})
                </button>
              )}
              <button
                type="button"
                class="inner-app-bar-btn"
                onClick={() => void load()}
                disabled={loading.value}
                title="重新加载当前 Inner App"
              >
                ↺ 热重载
              </button>
              <button
                type="button"
                class={['inner-app-bar-btn', 'accent', companionOpen.value && 'active']}
                onClick={() => { companionOpen.value = !companionOpen.value }}
                title="打开创造模式伴随悬浮窗，边用边改"
              >
                ✨ 伴随演进
              </button>
            </div>
          </div>

          {error.value
            ? <div class="inner-app-host-error">{error.value}</div>
            : html.value
              ? <iframe ref={frameRef} class="inner-app-frame" sandbox="allow-scripts allow-forms allow-popups" srcdoc={html.value}></iframe>
              : <div class="inner-app-host-empty">{loading.value ? '加载中...' : '暂无 UI'}</div>}

          {/* 创作伴随浮层 */}
          {companionOpen.value && (
            <div class="inner-app-companion-card">
              <div class="inner-app-companion-head">
                <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                  <span class="inner-app-companion-badge">Creative · 创造伴随窗</span>
                  <span style={{ fontSize: '11.5px', fontWeight: 600 }}>{props.appName}</span>
                </div>
                <button
                  type="button"
                  class="inner-app-companion-close"
                  onClick={() => { companionOpen.value = false }}
                >
                  ✕
                </button>
              </div>

              {runtimeErrors.value.length > 0 && (
                <div class="inner-app-companion-errbox">
                  <div style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    捕获异常：{runtimeErrors.value[0]}
                  </div>
                  <button
                    type="button"
                    class="inner-app-bar-btn warn"
                    disabled={evolving.value}
                    onClick={() => void evolveInPlace(`修复运行时报错：${runtimeErrors.value.join('; ')}`)}
                  >
                    一键热修复
                  </button>
                </div>
              )}

              <div class="inner-app-companion-chips">
                {['优化紧凑排版与配色', '增加数据导出与筛选栏', '适配深色模式与响应式布局'].map(chip => (
                  <button
                    key={chip}
                    type="button"
                    class="inner-app-companion-chip"
                    onClick={() => { companionPrompt.value = chip }}
                  >
                    {chip}
                  </button>
                ))}
              </div>

              <textarea
                class="inner-app-companion-input"
                rows={2}
                placeholder="边用边改：描述你想给当前应用增加的交互或修复点…"
                value={companionPrompt.value}
                onInput={(e: Event) => { companionPrompt.value = (e.target as HTMLTextAreaElement).value }}
                disabled={evolving.value}
              ></textarea>

              {evolveStatus.value && (
                <div class="inner-app-companion-status">{evolveStatus.value}</div>
              )}

              <div class="inner-app-companion-actions">
                <button
                  type="button"
                  class="inner-app-bar-btn accent"
                  disabled={evolving.value || !companionPrompt.value.trim()}
                  onClick={() => void evolveInPlace()}
                >
                  {evolving.value ? '正在热演进…' : '⚡ 原位热更新 Bundle'}
                </button>
                {props.onSendToAgent && (
                  <button
                    type="button"
                    class="inner-app-bar-btn"
                    disabled={!companionPrompt.value.trim()}
                    onClick={sendCompanionToMainChat}
                  >
                    ↗ 发送到主对话
                  </button>
                )}
              </div>
            </div>
          )}
        </div>
      )
    }
  },
})
