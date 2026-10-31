// AgentGo browser runtime. It stands in for the Wails desktop runtime
// (/wails/runtime.js) so the same frontend runs in a normal browser:
//   Call.ByName("pkg.Service.Method", ...args)  ->  POST /api/ipc/call
//   Events.On("chat:chunk", cb)                 ->  Server-Sent Events /api/ipc/events

function methodOf(name) {
  const i = name.lastIndexOf('.')
  return i >= 0 ? name.slice(i + 1) : name
}

export const Call = {
  ByName(name, ...args) {
    const ctrl = new AbortController()
    const p = fetch('/api/ipc/call', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ method: methodOf(String(name)), args }),
      signal: ctrl.signal,
    }).then(async (r) => {
      let body = null
      try { body = await r.json() } catch (e) { /* non-JSON error page */ }
      if (r.status === 401) throw new Error('未授权：请用 AgentGo 启动时打开的地址访问')
      if (!r.ok || !body || body.error) throw new Error((body && body.error) || ('HTTP ' + r.status))
      const res = body.result
      // Utility windows do not exist in a browser; the backend asks for a new tab.
      if (res && typeof res === 'object' && typeof res.open_url === 'string') {
        try { window.open(res.open_url, '_blank') } catch (e) { /* popup blocked */ }
      }
      return res
    })
    p.cancel = () => ctrl.abort()
    return p
  },
  ByID() {
    return Promise.reject(new Error('Call.ByID is not supported in the browser runtime'))
  },
}

const listeners = new Map()
let source = null

function ensureSource() {
  if (source) return
  source = new EventSource('/api/ipc/events')
  source.onmessage = (m) => {
    let ev
    try { ev = JSON.parse(m.data) } catch (e) { return }
    const set = listeners.get(ev.name)
    if (!set) return
    const evt = { name: ev.name, data: ev.data, sender: '', cancelled: false }
    for (const cb of Array.from(set)) {
      try { cb(evt) } catch (e) { console.error('[AgentGo] event listener failed:', e) }
    }
  }
}

function on(name, cb) {
  ensureSource()
  let set = listeners.get(name)
  if (!set) { set = new Set(); listeners.set(name, set) }
  set.add(cb)
  return () => { set.delete(cb) }
}

export const Events = {
  On: on,
  OnMultiple(name, cb) { return on(name, cb) },
  Once(name, cb) {
    const off = on(name, (e) => { off(); cb(e) })
    return off
  },
  Off(...names) { names.forEach((n) => listeners.delete(n)) },
  OffAll() { listeners.clear() },
  Emit() { return Promise.resolve(false) },
}

export default { Call, Events }
