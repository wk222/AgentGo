import { defineComponent, ref, onMounted, computed, type PropType } from 'vue'
import { Activity, ArrowLeft, ArrowRight, Bot, Check, CheckCircle2, Cpu, Eye, EyeOff, LoaderCircle, Moon, Palette, PlugZap, RefreshCw, Settings2, SlidersHorizontal, Sun, UserRound, AlertCircle } from 'lucide-vue-next'
import './settings.css'
import { wailsCall, wailsCallWithTimeout } from '../../shared/wails'
import { harnessList, getHarness, harnessOf, type HarnessId } from '../../shared/harness'
import AdvancedConfigCard from './AdvancedConfigCard'
import UsageCard from './UsageCard'
import DiagnosticsCard from './DiagnosticsCard'
import CallTraceCard from './CallTraceCard'
import ControlModeCard from './ControlModeCard'
import HooksCard from './HooksCard'
import IMCard from './IMCard'
import { PrivateAccountCard, privateProviderLabel } from './privateExtensions'

type Theme = 'light' | 'dark'

interface ProviderPreset {
  id: string
  name: string
  tag: string
  desc: string
  apiBase: string
  model: string
  fallback: string
  specialLoader?: 'deepseek-reasoner' | 'deepseek-chat' | 'azure-luna'
}

const PROVIDER_PRESETS: ProviderPreset[] = [
  {
    id: 'deepseek-reasoner',
    name: 'DeepSeek Reasoner',
    tag: '推荐',
    desc: '原生思维链推理 + 前缀缓存友好，适合复杂架构与重构任务',
    apiBase: 'https://api.deepseek.com/v1',
    model: 'deepseek-reasoner',
    fallback: 'deepseek-chat',
    specialLoader: 'deepseek-reasoner',
  },
  {
    id: 'deepseek-chat',
    name: 'DeepSeek V3 / Chat',
    tag: '极速低延迟',
    desc: '高吞吐工具调用与日常编码交互，自动回退 Reasoner',
    apiBase: 'https://api.deepseek.com/v1',
    model: 'deepseek-chat',
    fallback: 'deepseek-reasoner',
    specialLoader: 'deepseek-chat',
  },
  {
    id: 'azure-luna',
    name: 'Azure GPT-6 Luna',
    tag: '企业级网关',
    desc: '读取环境变量 AGENTGO_AZURE_*，高配额稳定推理',
    apiBase: '',
    model: 'gpt-6-luna',
    fallback: '',
    specialLoader: 'azure-luna',
  },
  {
    id: 'openai',
    name: 'OpenAI Official',
    tag: '标准兼容',
    desc: 'OpenAI v1 标准端点，默认搭载 gpt-4o 与 gpt-4o-mini',
    apiBase: 'https://api.openai.com/v1',
    model: 'gpt-4o',
    fallback: 'gpt-4o-mini',
  },
  {
    id: 'ollama',
    name: 'Ollama Local',
    tag: '离线本地',
    desc: '连接本机 11434 端口，零外部 Token 消耗运行开源权重',
    apiBase: 'http://127.0.0.1:11434/v1',
    model: 'qwen2.5-coder:14b',
    fallback: '',
  },
]

export const SettingsPanel = defineComponent({
  name: 'SettingsPanel',
  props: {
    theme: { type: String as PropType<Theme>, required: true },
    onToggleTheme: { type: Function as PropType<() => void>, required: true },
    modeProfile: { type: String, required: true },
    executionCanvas: { type: String, required: true },
    onModeChange: { type: Function as PropType<(profile: string, canvas: string) => void>, required: true },
    onSaved: { type: Function as PropType<() => void>, default: undefined },
    onClose: { type: Function as PropType<() => void>, default: undefined },
  },
  setup(props) {
    const section = ref<'account' | 'models' | 'agent' | 'appearance' | 'usage' | 'advanced'>(PrivateAccountCard ? 'account' : 'models')
    const showKey = ref(false)
    const connection = ref<'idle' | 'ok' | 'error'>('idle')
    const saved = ref({ base: '', model: '', fallback: '' })
    const apiBase = ref('')
    const apiKey = ref('')
    const apiKeyHint = ref('')
    const model = ref('')
    const fallbackModel = ref('')
    const hasAzureLuna = ref(false)
    const azureModel = ref('gpt-6-luna')
    const hasDeepSeek = ref(false)
    const saving = ref(false)
    const testing = ref(false)
    const status = ref<{ ok?: boolean; msg: string } | null>(null)
    const profile = ref(props.modeProfile || 'assistant')
    const canvas = ref(props.executionCanvas || 'balanced')
    const modeSaving = ref(false)

    const dirty = computed(() => !!apiKey.value || apiBase.value !== saved.value.base || model.value !== saved.value.model || fallbackModel.value !== saved.value.fallback)
    const providerName = computed(() => {
      const privateLabel = privateProviderLabel(apiBase.value, model.value)
      if (privateLabel) return privateLabel
      return PROVIDER_PRESETS.find(p => p.id === activePresetId.value)?.name || '自定义连接'
    })
    const activePresetId = computed(() => {
      const b = apiBase.value.toLowerCase()
      const m = model.value.toLowerCase()
      if (b.includes('deepseek.com') && m.includes('reasoner')) return 'deepseek-reasoner'
      if (b.includes('deepseek.com')) return 'deepseek-chat'
      if (b.includes('azure') || m.includes('luna')) return 'azure-luna'
      if (b.includes('11434') || b.includes('ollama')) return 'ollama'
      if (b.includes('openai.com')) return 'openai'
      return ''
    })

    const load = async () => {
      try {
        const [cfg, flags] = await Promise.all([
          wailsCall('GetLLMConfig'),
          wailsCall('GetFeatureFlags'),
        ])
        if (cfg) {
          apiBase.value = cfg.api_base || ''
          model.value = cfg.model || ''
          fallbackModel.value = cfg.fallback_model || ''
          apiKeyHint.value = cfg.api_key_hint || (cfg.api_key_set ? '已设置' : '未设置')
          hasAzureLuna.value = !!cfg.has_azure_luna
          hasDeepSeek.value = !!cfg.has_deepseek
          if (cfg.azure_model) azureModel.value = cfg.azure_model
          saved.value = { base: apiBase.value, model: model.value, fallback: fallbackModel.value }
        }
        if (flags) {
          profile.value = flags.mode_profile || props.modeProfile || 'assistant'
          canvas.value = flags.execution_canvas || props.executionCanvas || 'balanced'
        }
      } catch (e: any) {
        status.value = { ok: false, msg: e?.message || '读取配置失败' }
      }
    }

    const applyPreset = async (preset: ProviderPreset) => {
      saving.value = true
      status.value = null
      try {
        if (preset.specialLoader === 'deepseek-reasoner' || preset.specialLoader === 'deepseek-chat') {
          const res = await wailsCallWithTimeout('LoadDeepSeekConfig', 10000, preset.model)
          if (res?.success === false) {
            status.value = { ok: false, msg: res.error || '切换 DeepSeek 预设失败' }
          } else {
            await load()
            props.onSaved?.()
            status.value = {
              ok: true,
              msg: res?.from_env_key
                ? `已切换至 ${preset.name}（自动载入 DEEPSEEK_API_KEY 环境变量）`
                : `已填入 ${preset.name} 端点与模型，请补充 API Key 后保存`,
            }
          }
          return
        }
        if (preset.specialLoader === 'azure-luna') {
          const res = await wailsCallWithTimeout('LoadAzureLunaConfig', 10000)
          if (res?.success === false) {
            status.value = { ok: false, msg: res.error || '加载 Azure Luna 失败' }
          } else {
            await load()
            props.onSaved?.()
            status.value = { ok: true, msg: `已切换至 Azure (${res?.model || azureModel.value})` }
          }
          return
        }
        apiBase.value = preset.apiBase
        model.value = preset.model
        fallbackModel.value = preset.fallback
        status.value = { ok: true, msg: `已套用 ${preset.name} 预设参数，点击「保存配置」生效` }
      } catch (e: any) {
        status.value = { ok: false, msg: e?.message || '套用预设失败' }
      } finally {
        saving.value = false
      }
    }

    const saveLLM = async () => {
      if (!apiBase.value.trim() || !model.value.trim()) { status.value = { ok: false, msg: '请填写 API 地址和主模型' }; return }
      saving.value = true
      status.value = null
      connection.value = 'idle'
      try {
        const res = await wailsCallWithTimeout(
          'SetLLMConfig',
          10000,
          apiBase.value.trim(),
          apiKey.value.trim(),
          model.value.trim(),
          fallbackModel.value.trim(),
        )
        if (res?.success === false) {
          status.value = { ok: false, msg: res.error || '保存失败' }
        } else {
          apiKey.value = ''
          await load()
          props.onSaved?.()
          status.value = { ok: true, msg: '连接配置已保存' }
        }
      } catch (e: any) {
        status.value = { ok: false, msg: e?.message || '保存失败' }
      } finally {
        saving.value = false
      }
    }

    const testLLM = async () => {
      if (dirty.value) { status.value = { ok: false, msg: '请先保存连接，再测试模型' }; return }
      testing.value = true
      status.value = null
      try {
        const res = await wailsCallWithTimeout('TestLLM', 65000)
        connection.value = res?.ok ? 'ok' : 'error'
        status.value = { ok: !!res?.ok, msg: res?.message || '测试完成' }
      } catch (e: any) {
        connection.value = 'error'
        status.value = { ok: false, msg: e?.message || '连接测试失败' }
      } finally {
        testing.value = false
      }
    }

    const activeHarnessId = computed(() => harnessOf(profile.value, canvas.value)?.id ?? `${profile.value} · ${canvas.value}`)

    const selectHarnessPreset = async (id: HarnessId) => {
      const h = getHarness(id)
      profile.value = h.profile
      canvas.value = h.canvas
      await saveMode()
    }

    const saveMode = async () => {
      modeSaving.value = true
      try {
        await props.onModeChange(profile.value, canvas.value)
        status.value = { ok: true, msg: '默认协作方式已更新' }
      } catch (e: any) {
        status.value = { ok: false, msg: e?.message || '模式更新失败' }
      } finally {
        modeSaving.value = false
      }
    }

    onMounted(load)

    const allNavigation = [
      { id: 'account', label: '账号', detail: '企业账号与登录', icon: UserRound },
      { id: 'models', label: '模型连接', detail: '服务商与 API', icon: Cpu },
      { id: 'agent', label: '协作方式', detail: '默认工作模式', icon: Bot },
      { id: 'appearance', label: '外观', detail: '界面主题', icon: Palette },
      { id: 'usage', label: '用量与诊断', detail: 'Token 与排障', icon: Activity },
      { id: 'advanced', label: '高级配置', detail: '运行参数与覆盖', icon: SlidersHorizontal },
    ] as const
    const navigation = allNavigation.filter(item => item.id !== 'account' || !!PrivateAccountCard)
    const headings = {
      account: ['账号与工作空间', '连接企业账号，使用工作空间提供的模型。'],
      models: ['模型连接', '选择模型服务，并验证实际响应。'],
      agent: ['协作方式', '为不同类型的任务选择合适的默认模式。'],
      appearance: ['外观', '让工作空间适合你的阅读习惯。'],
      usage: ['用量与诊断', '查看模型用了多少 Token；出问题时一键生成诊断包。'],
      advanced: ['高级配置', '查看当前生效的运行参数和环境覆盖。'],
    }

    return () => <div class={['panel-wrap settings-page', props.theme === 'dark' ? 'settings-is-dark' : '']}>
      <header class="settings-topbar"><div class="settings-title"><Settings2 size={19} /><span>设置</span><span class="settings-topbar-divider" />偏好与连接</div>{props.onClose && <button class="settings-quiet" onClick={props.onClose}><ArrowLeft size={14} />返回工作区</button>}</header>
      <div class="settings-shell">
        <aside class="settings-navigation"><div class="settings-nav-caption">工作空间偏好</div><nav aria-label="设置分类">{navigation.map(item => <button key={item.id} class={['settings-nav-item', section.value === item.id ? 'active' : '']} aria-current={section.value === item.id ? 'page' : undefined} onClick={() => { section.value = item.id; status.value = null }}><item.icon size={17} strokeWidth={1.7} /><span><strong>{item.label}</strong><small>{item.detail}</small></span>{section.value === item.id && <span class="settings-nav-dot" />}</button>)}</nav><div class="settings-nav-footer"><span class="settings-app-monogram">a</span><div><strong>AgentGo</strong><small>你的工作空间，由你定义</small></div></div></aside>
        <main class="settings-main"><div class="settings-content">
          <div class="settings-heading"><div><div class="settings-breadcrumb">设置 / {navigation.find(n => n.id === section.value)?.label}</div><h2>{headings[section.value][0]}</h2><p>{headings[section.value][1]}</p></div><button class="settings-icon-button" title="刷新配置" aria-label="刷新配置" onClick={load}><RefreshCw size={16} /></button></div>
          <div class="settings-connection-summary"><span class={['settings-connection-icon', connection.value]}><PlugZap size={17} /></span><div><span>当前模型<span class="settings-provider-label">{providerName.value}</span></span><strong title={model.value}>{model.value || '尚未配置模型'}</strong></div><span class={['settings-health', connection.value]}><i />{connection.value === 'ok' ? '测试通过' : connection.value === 'error' ? '连接异常' : '待验证'}</span><button class="settings-text-action" disabled={testing.value || dirty.value || !model.value} onClick={testLLM}>{testing.value ? <LoaderCircle size={14} class="settings-spin" /> : <ArrowRight size={14} />}{testing.value ? '验证中' : '测试模型'}</button></div>
          {status.value && <div class={['settings-feedback', status.value.ok ? 'success' : 'error']} role="status">{status.value.ok ? <CheckCircle2 size={16} /> : <AlertCircle size={16} />}<span>{status.value.msg}</span></div>}
          <div class="settings-section" style={{ display: section.value === 'account' ? '' : 'none' }}>{PrivateAccountCard && <PrivateAccountCard onConnected={() => { apiKey.value = ''; connection.value = 'idle'; void load(); props.onSaved?.() }} />}<div class="settings-inline-note"><Cpu size={15} /><span>使用其他模型服务？</span><button onClick={() => { section.value = 'models' }}>配置 API 连接<ArrowRight size={13} /></button></div></div>
          {section.value === 'models' && <div class="settings-section">
            <section class="settings-surface"><div class="settings-surface-heading"><div><h3>模型服务</h3><p>选择预设以填入连接参数，也可以使用自定义接口。</p></div>{hasDeepSeek.value && <span class="settings-soft-tag">已检测到 DeepSeek 密钥</span>}</div><div class="settings-providers">{PROVIDER_PRESETS.map((preset, index) => <button key={preset.id} class={['settings-provider', activePresetId.value === preset.id ? 'selected' : '']} disabled={saving.value} onClick={() => applyPreset(preset)}><span class={['settings-provider-symbol', 'provider-' + index]}>{preset.name.slice(0, 1)}</span><span><strong>{preset.name.replace(' Official', '').replace(' Local', '')}</strong><small>{preset.id === 'ollama' ? '本地运行' : preset.id === 'azure-luna' ? '企业网关' : 'API 连接'}</small></span>{activePresetId.value === preset.id && <Check size={14} />}</button>)}</div></section>
            <section class="settings-surface"><div class="settings-surface-heading"><div><h3>连接参数</h3><p>兼容 OpenAI 格式的接口。密钥留空会保留已保存的值。</p></div>{dirty.value && <span class="settings-unsaved">尚未保存</span>}</div><div class="settings-fields"><label class="settings-field wide"><span>API 地址</span><input value={apiBase.value} onInput={(e: any) => { apiBase.value = e.target.value }} placeholder="https://api.example.com/v1" spellcheck={false} /><small>填写服务商提供的 API Base URL。</small></label><label class="settings-field wide"><span>API 密钥<span class="settings-field-hint">{apiKeyHint.value || '未设置'}</span></span><div class="settings-secret-input"><input type={showKey.value ? 'text' : 'password'} value={apiKey.value} onInput={(e: any) => { apiKey.value = e.target.value }} placeholder="输入新密钥，留空保持不变" autocomplete="off" spellcheck={false} /><button type="button" aria-label={showKey.value ? '隐藏密钥' : '显示密钥'} onClick={() => { showKey.value = !showKey.value }}>{showKey.value ? <EyeOff size={16} /> : <Eye size={16} />}</button></div></label><label class="settings-field"><span>主模型</span><input value={model.value} onInput={(e: any) => { model.value = e.target.value }} placeholder="模型 ID" spellcheck={false} /></label><label class="settings-field"><span>备用模型<span class="settings-optional">可选</span></span><input value={fallbackModel.value} onInput={(e: any) => { fallbackModel.value = e.target.value }} placeholder="留空不使用备用模型" spellcheck={false} /></label></div><footer class="settings-surface-footer"><span>保存后可发送一次短请求，验证鉴权和模型响应。</span><div><button class="settings-secondary" disabled={testing.value || dirty.value} onClick={testLLM}>{testing.value ? <LoaderCircle size={14} class="settings-spin" /> : <PlugZap size={14} />}测试模型</button><button class="settings-primary" disabled={saving.value} onClick={saveLLM}>{saving.value ? <LoaderCircle size={14} class="settings-spin" /> : <Check size={14} />}{saving.value ? '保存中' : '保存连接'}</button></div></footer></section>
          </div>}
          {section.value === 'agent' && <div class="settings-section"><section class="settings-surface"><div class="settings-surface-heading"><div><h3>默认协作方式</h3><p>选择适合当前工作习惯的模式，也可以在对话中随时切换。</p></div></div><div class="settings-mode-list">{harnessList().map(mode => <button key={mode.id} class={['settings-mode', activeHarnessId.value === mode.id ? 'selected' : '']} disabled={modeSaving.value} onClick={() => selectHarnessPreset(mode.id)}><span class="settings-mode-radio">{activeHarnessId.value === mode.id && <span />}</span><span><strong>{mode.name || mode.label}</strong><small>{mode.desc}</small></span>{activeHarnessId.value === mode.id && <span class="settings-soft-tag">当前模式</span>}</button>)}</div></section><ControlModeCard /><IMCard /><HooksCard /></div>}
          {section.value === 'appearance' && <section class="settings-surface"><div class="settings-surface-heading"><div><h3>界面主题</h3><p>为整个应用选择明亮或深色的界面。</p></div></div><div class="settings-theme-options">{(['light', 'dark'] as const).map(theme => <button key={theme} class={['settings-theme-option', props.theme === theme ? 'selected' : '']} aria-pressed={props.theme === theme} onClick={() => { if (props.theme !== theme) props.onToggleTheme() }}><div class={['settings-theme-sample', theme]}><span class="sample-sidebar"><i /><i /><i /></span><span class="sample-content"><i /><i /><i /><em /></span></div><span class="settings-theme-caption">{theme === 'light' ? <Sun size={15} /> : <Moon size={15} />}<strong>{theme === 'light' ? '浅色' : '深色'}</strong><span class="settings-mode-radio">{props.theme === theme && <span />}</span></span></button>)}</div></section>}
          {section.value === 'usage' && <div class="settings-section"><UsageCard /><DiagnosticsCard /><CallTraceCard /></div>}
          {section.value === 'advanced' && <AdvancedConfigCard />}
        </div></main>
      </div>
    </div>
  },
})
