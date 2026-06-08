import { defineComponent, onMounted, onUnmounted, ref, watch } from 'vue'
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

export default defineComponent({
  name: 'InnerAppHost',
  props: {
    appName: { type: String, required: true },
    title: { type: String, default: '' },
  },
  setup(props) {
    const html = ref('')
    const loading = ref(false)
    const error = ref('')
    const frameRef = ref<HTMLIFrameElement | null>(null)
    const session = ref<any | null>(null)
    const manifest = ref<any | null>(null)

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

    const load = async () => {
      const name = props.appName.trim()
      if (!name) return
      loading.value = true
      error.value = ''
      html.value = ''
      session.value = null
      manifest.value = null
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
          html.value = await prepareHTML(name, r.html || '', hostSession)
        } else {
          error.value = r?.error || '打开 Inner App UI 失败'
        }
      } catch (e: any) {
        error.value = e?.message || '打开 Inner App UI 失败'
      } finally {
        loading.value = false
      }
    }

    const handleInnerAppMessage = async (ev: MessageEvent) => {
      const msg: any = ev.data || {}
      if (msg.type !== 'agentgo:inner-app-api') return
      const source = ev.source as Window | null
      if (!source) return
      if (frameRef.value?.contentWindow && source !== frameRef.value.contentWindow) return
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
    watch(() => props.appName, () => { void load() })

    return () => (
      <div class="inner-app-host">
        {props.title && (
          <div class="inner-app-host-head">
            <span>{props.title}</span>
            {loading.value && <span class="inner-app-host-status">加载中...</span>}
          </div>
        )}
        {manifest.value && (
          <div class="inner-app-manifest-bar">
            <span>{manifest.value.export_policy === 'open' ? 'open policy' : 'exports'}</span>
            {(manifest.value.actions || []).slice(0, 8).map((a: any) => (
              <code key={String(a.name || a)}>{String(a.name || a)}</code>
            ))}
          </div>
        )}
        {error.value
          ? <div class="inner-app-host-error">{error.value}</div>
          : html.value
            ? <iframe ref={frameRef} class="inner-app-frame" sandbox="allow-scripts allow-forms allow-popups" srcdoc={html.value}></iframe>
            : <div class="inner-app-host-empty">{loading.value ? '加载中...' : '暂无 UI'}</div>}
      </div>
    )
  },
})
