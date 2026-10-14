import { defineComponent, ref, onMounted, computed, type PropType } from 'vue'
import { wailsCall, wailsCallWithTimeout } from '../wails'

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
    tag: 'Reasonix 推荐',
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
          wailsCall<any>('GetLLMConfig'),
          wailsCall<any>('GetFeatureFlags'),
        ])
        if (cfg) {
          apiBase.value = cfg.api_base || ''
          model.value = cfg.model || ''
          fallbackModel.value = cfg.fallback_model || ''
          apiKeyHint.value = cfg.api_key_hint || (cfg.api_key_set ? '已设置' : '未设置')
          hasAzureLuna.value = !!cfg.has_azure_luna
          hasDeepSeek.value = !!cfg.has_deepseek
          if (cfg.azure_model) azureModel.value = cfg.azure_model
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
      saving.value = true
      status.value = null
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
          status.value = { ok: true, msg: 'LLM 配置已保存并热更新至运行时' }
        }
      } catch (e: any) {
        status.value = { ok: false, msg: e?.message || '保存失败' }
      } finally {
        saving.value = false
      }
    }

    const testLLM = async () => {
      testing.value = true
      status.value = null
      try {
        const res = await wailsCallWithTimeout('TestLLM', 20000)
        status.value = { ok: !!res?.ok, msg: res?.message || res?.error || '测试完成' }
      } catch (e: any) {
        status.value = { ok: false, msg: e?.message || '连接测试失败' }
      } finally {
        testing.value = false
      }
    }

    const saveMode = async () => {
      modeSaving.value = true
      try {
        await props.onModeChange(profile.value, canvas.value)
        status.value = { ok: true, msg: `默认协作模式已更新为 ${profile.value} · ${canvas.value}` }
      } catch (e: any) {
        status.value = { ok: false, msg: e?.message || '模式更新失败' }
      } finally {
        modeSaving.value = false
      }
    }

    onMounted(load)

    return () => (
      <div class="panel-wrap">
        <div class="panel-header">
          <div class="panel-title">
            <span>系统与模型配置</span>
            <span class="panel-count">Runtime & Reasonix Harness</span>
          </div>
          <div class="panel-actions">
            <button class="btn-secondary" onClick={load}>刷新状态</button>
            {props.onClose && (
              <button class="btn-secondary" onClick={props.onClose}>返回工作区</button>
            )}
          </div>
        </div>

        <div class="panel-body">
          <div class="settings-container">
            {status.value && (
              <div class={['settings-status', status.value.ok ? 'ok' : 'err']}>
                {status.value.msg}
              </div>
            )}

            <div class="settings-card">
              <div class="settings-card-title">模型提供商快速预设 (Provider Presets)</div>
              <div class="settings-card-sub">
                借鉴 DeepSeek-Reasonix 的多端点预设体系，支持一键切换推理模型与本地/云端网关。
                {hasDeepSeek.value && '（已检测到 DEEPSEEK_API_KEY）'}
              </div>
              <div class="provider-presets-grid">
                {PROVIDER_PRESETS.map(p => (
                  <button
                    key={p.id}
                    type="button"
                    class={['provider-preset-card', activePresetId.value === p.id ? 'active' : '']}
                    disabled={saving.value}
                    onClick={() => applyPreset(p)}
                  >
                    <div class="provider-preset-head">
                      <span class="provider-preset-name">{p.name}</span>
                      <span class="provider-preset-tag">{p.tag}</span>
                    </div>
                    <div class="provider-preset-desc">{p.desc}</div>
                  </button>
                ))}
              </div>
            </div>

            <div class="settings-card">
              <div class="settings-card-title">LLM 连接参数</div>
              <div class="settings-card-sub">
                兼容所有 OpenAI / DeepSeek / Azure / Ollama `/v1` 接口；留空 API Key 时将保留当前已存储密钥（状态：{apiKeyHint.value || '未设置'}）。
              </div>
              <div class="settings-grid">
                <label class="form-field">
                  <span class="form-label">API Base URL</span>
                  <input
                    class="form-input"
                    value={apiBase.value}
                    onInput={(e: any) => { apiBase.value = e.target.value }}
                    placeholder="https://api.deepseek.com/v1"
                  />
                </label>
                <label class="form-field">
                  <span class="form-label">API Key（留空保持不变）</span>
                  <input
                    class="form-input"
                    type="password"
                    value={apiKey.value}
                    onInput={(e: any) => { apiKey.value = e.target.value }}
                    placeholder={apiKeyHint.value || 'sk-...'}
                  />
                </label>
                <label class="form-field">
                  <span class="form-label">主模型 (Primary Model)</span>
                  <input
                    class="form-input"
                    value={model.value}
                    onInput={(e: any) => { model.value = e.target.value }}
                    placeholder="deepseek-reasoner / gpt-4o"
                  />
                </label>
                <label class="form-field">
                  <span class="form-label">回退模型 (Fallback Model)</span>
                  <input
                    class="form-input"
                    value={fallbackModel.value}
                    onInput={(e: any) => { fallbackModel.value = e.target.value }}
                    placeholder="deepseek-chat / gpt-4o-mini"
                  />
                </label>
              </div>
              <div class="settings-actions">
                <button class="btn-primary" disabled={saving.value} onClick={saveLLM}>
                  {saving.value ? '保存中…' : '保存配置'}
                </button>
                <button class="btn-secondary" disabled={testing.value} onClick={testLLM}>
                  {testing.value ? '测试中…' : '测试连通性'}
                </button>
              </div>
            </div>

            <div class="settings-card">
              <div class="settings-card-title">Cache-First 上下文卫生与默认协作模式</div>
              <div class="settings-card-sub">
                已启用 Reasonix 风格前缀缓存保护：最近 6 轮保持原始工具输出，历史超长工具输出自动按头尾比例确定性裁剪（保留全部 Error 诊断），最大化 DeepSeek / OpenAI 缓存命中率。
              </div>
              <div class="settings-grid">
                <label class="form-field">
                  <span class="form-label">默认角色边界 (Profile)</span>
                  <select class="form-input" value={profile.value} onChange={(e: any) => { profile.value = e.target.value }}>
                    <option value="assistant">Assistant（通用工程助手）</option>
                    <option value="matrix">App Matrix（偏重 InnerApp 与工具编排）</option>
                    <option value="admin">Admin（开放高权限运维工具）</option>
                  </select>
                </label>
                <label class="form-field">
                  <span class="form-label">默认执行深度 (Execution Canvas)</span>
                  <select class="form-input" value={canvas.value} onChange={(e: any) => { canvas.value = e.target.value }}>
                    <option value="focused">Focused（紧凑快速响应）</option>
                    <option value="balanced">Balanced（平衡规划与执行）</option>
                    <option value="deep">Deep（深度规划、验证与自检）</option>
                  </select>
                </label>
              </div>
              <div class="settings-actions">
                <button class="btn-primary" disabled={modeSaving.value} onClick={saveMode}>
                  {modeSaving.value ? '应用中…' : '应用默认模式'}
                </button>
                <button class="btn-secondary" onClick={props.onToggleTheme}>
                  切换外观（当前：{props.theme === 'dark' ? '深色碳黑' : '浅色暖白'}）
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>
    )
  },
})
