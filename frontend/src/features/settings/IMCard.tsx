import { defineComponent, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { LoaderCircle, QrCode, RefreshCw, Trash2 } from 'lucide-vue-next'
import { wailsCall } from '../../shared/wails'
import { notify } from '../../shared/ui/notify'
import type { IMChannel, IMLoginState, IMPlatform, IMStatus } from '../../shared/ipc/types'
import { IM_EXAMPLE_NOTE, buildSaveRequest, channelSummary, formatAllowFrom, loginActive, loginText } from './imModel'
import './im-card.css'

interface Form { enabled: boolean; allowAll: boolean; allowFrom: string; values: Record<string, string> }

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e))

// 设置 / 聊天软件：把 Telegram、飞书、钉钉、企业微信、微信、Discord 的机器人接进来，
// 在手机的聊天软件里直接给 AgentGo 发消息、批准操作。配置写在 <数据目录>/channels.yml，保存后立即重连。
export default defineComponent({
  name: 'IMCard',
  setup() {
    const status = ref<IMStatus | null>(null)
    const loading = ref(true)
    const busy = ref('')
    const error = ref('')
    const open = ref('')
    const forms = reactive<Record<string, Form>>({})
    const login = ref<IMLoginState>({ status: 'idle' })
    let pollTimer = 0

    const channelOf = (id: string): IMChannel | undefined => status.value?.channels.find(c => c.platform === id)

    const fillForm = (p: IMPlatform) => {
      const c = channelOf(p.id)
      const values: Record<string, string> = {}
      for (const f of p.fields ?? []) values[f.key] = f.secret ? '' : c?.fields[f.key] ?? ''
      forms[p.id] = { enabled: c ? c.enabled : true, allowAll: c?.allow_all ?? false, allowFrom: formatAllowFrom(c?.allow_from), values }
    }

    const apply = (next: IMStatus) => {
      if (!next || typeof next !== 'object' || !Array.isArray(next.platforms)) throw new Error('后端没有返回聊天软件状态，可能需要更新到新版本。')
      status.value = next
      if (next.error) error.value = next.error
    }

    const load = async () => {
      try {
        apply(await wailsCall('GetIMStatus'))
        error.value = status.value?.error ?? ''
      } catch (e) {
        error.value = errText(e)
      } finally {
        loading.value = false
      }
    }
    onMounted(() => { void load() })

    const toggle = (p: IMPlatform) => {
      if (open.value === p.id) { open.value = ''; return }
      fillForm(p)
      open.value = p.id
    }

    const run = async (key: string, fn: () => Promise<IMStatus>, okText: string) => {
      if (busy.value) return
      busy.value = key
      error.value = ''
      try {
        apply(await fn())
        notify.success(okText)
        return true
      } catch (e) {
        error.value = errText(e)
        return false
      } finally {
        busy.value = ''
      }
    }

    const save = async (p: IMPlatform) => {
      const f = forms[p.id]
      if (!f) return
      if (await run(`save:${p.id}`, () => wailsCall('SaveIMChannel', buildSaveRequest(p, f)), `${p.label} 已保存`)) open.value = ''
    }
    const remove = async (p: IMPlatform) => {
      if (!window.confirm(`删除 ${p.label} 的配置？机器人会断开。`)) return
      if (await run(`rm:${p.id}`, () => wailsCall('RemoveIMChannel', p.id), `${p.label} 已删除`)) open.value = ''
    }
    const reload = () => run('reload', () => wailsCall('ReloadIM'), '已重新连接')

    const stopPoll = () => { window.clearInterval(pollTimer); pollTimer = 0 }
    const poll = async () => {
      try {
        const st = await wailsCall('GetWeixinLoginState')
        login.value = st
        if (!loginActive(st)) {
          stopPoll()
          if (st.status === 'done') { notify.success('微信登录成功'); void load() }
        }
      } catch (e) {
        stopPoll()
        login.value = { status: 'error', message: errText(e) }
      }
    }
    const startLogin = async () => {
      stopPoll()
      try {
        login.value = await wailsCall('StartWeixinLogin')
        pollTimer = window.setInterval(() => { void poll() }, 1500)
      } catch (e) {
        login.value = { status: 'error', message: errText(e) }
      }
    }
    const cancelLogin = async () => {
      stopPoll()
      try { login.value = await wailsCall('CancelWeixinLogin') } catch { login.value = { status: 'idle' } }
    }
    onBeforeUnmount(stopPoll)

    const form = (p: IMPlatform) => {
      const f = forms[p.id]
      const c = channelOf(p.id)
      if (!f) return null
      return <div class="im-form">
        {p.help && <p class="im-help">{p.help}</p>}
        {(p.fields ?? []).length > 0 && <div class="settings-fields">
          {(p.fields ?? []).map(fld => <label class="settings-field" key={fld.key}>
            <span>{fld.label}{fld.secret && <i class="settings-optional">{c?.secrets_set[fld.key] ? '已填写，留空保持不变' : '必填'}</i>}</span>
            <input type={fld.secret ? 'password' : 'text'} autocomplete="off" spellcheck={false}
              placeholder={fld.secret && c?.secrets_set[fld.key] ? '••••••••' : ''}
              value={f.values[fld.key]} onInput={(e: Event) => { f.values[fld.key] = (e.target as HTMLInputElement).value }} />
            {fld.hint && <small>{fld.hint}</small>}
          </label>)}
        </div>}
        {p.qr_login && <div class="im-login">
          <div class="im-login-head">
            <strong>微信扫码登录</strong>
            <span class={['im-chip', c?.logged_in ? 'ok' : 'off']}>{c?.logged_in ? '已登录' : '未登录'}</span>
            {loginActive(login.value)
              ? <button type="button" class="settings-secondary" onClick={() => void cancelLogin()}>取消</button>
              : <button type="button" class="settings-secondary" onClick={() => void startLogin()}><QrCode size={14} />{c?.logged_in ? '重新登录' : '扫码登录'}</button>}
          </div>
          {login.value.status !== 'idle' && <div class={['im-login-state', login.value.status]} role="status">
            {loginActive(login.value) && login.value.qr_data_url && <img class="im-qr" src={login.value.qr_data_url} alt="微信登录二维码" />}
            <span>{loginText(login.value)}{login.value.status === 'done' && login.value.user_id ? `（只允许这个微信号：${login.value.user_id}）` : ''}</span>
          </div>}
        </div>}
        <div class="settings-fields">
          <label class="settings-field wide">
            <span>允许的用户<i class="settings-optional">用户 ID 或会话 ID，逗号或换行分隔</i></span>
            <textarea class="im-textarea" rows={3} spellcheck={false} value={f.allowFrom}
              placeholder="留空 = 拒绝所有人（被拒绝的人会收到自己的 ID，方便你填进来）"
              onInput={(e: Event) => { f.allowFrom = (e.target as HTMLTextAreaElement).value }} />
          </label>
        </div>
        <label class="im-check"><input type="checkbox" checked={f.allowAll} onChange={(e: Event) => { f.allowAll = (e.target as HTMLInputElement).checked }} />
          <span>任何人都能用<em>危险：谁找到机器人谁就能让它读写文件、执行命令</em></span></label>
        <label class="im-check"><input type="checkbox" checked={f.enabled} onChange={(e: Event) => { f.enabled = (e.target as HTMLInputElement).checked }} />
          <span>启用</span></label>
        <div class="im-actions">
          <button type="button" class="settings-primary" disabled={!!busy.value} onClick={() => void save(p)}>
            {busy.value === `save:${p.id}` && <LoaderCircle size={14} class="settings-spin" />}保存并连接
          </button>
          {c && <button type="button" class="settings-secondary im-danger" disabled={!!busy.value} onClick={() => void remove(p)}><Trash2 size={13} />删除</button>}
        </div>
      </div>
    }

    return () => {
      const s = status.value
      return <section class="settings-surface im-card">
        <div class="settings-surface-heading">
          <div><h3>聊天软件</h3><p>把机器人接进 Telegram、飞书、钉钉、企业微信、微信、Discord：在手机上直接给 AgentGo 发消息，需要批准时在聊天里点按钮。</p></div>
          <button class="settings-secondary im-reload" disabled={!!busy.value || loading.value} onClick={() => void reload()}>
            {busy.value === 'reload' ? <LoaderCircle size={14} class="settings-spin" /> : <RefreshCw size={14} />}重新连接
          </button>
        </div>
        {error.value && <div class="settings-feedback error" role="status">{error.value}</div>}
        {loading.value && !s && <div class="im-none">正在读取…</div>}
        {s && <>
          {s.disabled && <div class="im-banner" role="status">{s.disabled_reason}</div>}
          <div class="im-warning">{IM_EXAMPLE_NOTE}</div>
          <ul class="im-list">
            {s.platforms.map(p => {
              const c = channelOf(p.id)
              const sum = channelSummary(p, c)
              return <li key={p.id} class={['im-item', open.value === p.id ? 'open' : '']}>
                <button type="button" class="im-row" aria-expanded={open.value === p.id} onClick={() => toggle(p)}>
                  <i class={['im-dot', sum.tone]} aria-hidden="true" />
                  <strong>{p.label}</strong>
                  <span class="im-sum">{sum.text}</span>
                  <em>{open.value === p.id ? '收起' : c ? '修改' : '配置'}</em>
                </button>
                {open.value === p.id && form(p)}
              </li>
            })}
          </ul>
          <p class="im-path">配置文件：<code>{s.config_path}</code>（密钥保存在这里，界面不会再显示）。机器人里发 /help 查看命令：/new 新会话、/stop 中止、/approve 批准、/deny 拒绝。</p>
        </>}
      </section>
    }
  },
})
