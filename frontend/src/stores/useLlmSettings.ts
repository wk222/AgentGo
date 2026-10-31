// LLM 配置：从 App.tsx 抽出，逻辑不变。密钥只提交，不回显（后端只返回 api_key_set）。
import { ref } from 'vue'
import { wailsCall } from '../shared/wails'

const form = ref({ api_base: 'https://api.openai.com/v1', api_key: '', model: 'gpt-4o', fallback_model: '' })
const status = ref<{ label: string; ok: boolean | null }>({ label: '未检测', ok: null })
const testing = ref(false)
const testResult = ref<{ ok: boolean; message?: string } | null>(null)
const hasAzureLuna = ref(false)
const azureLunaInfo = ref({ base: '', model: '' })

async function load() {
  try {
    const cfg = await wailsCall('GetLLMConfig')
    if (!cfg) return
    form.value.api_base = cfg.api_base || form.value.api_base
    form.value.model = cfg.model || form.value.model
    form.value.fallback_model = cfg.fallback_model || ''
    status.value = { label: cfg.api_key_set ? 'API 已配置' : '未配置 Key', ok: !!cfg.api_key_set }
    hasAzureLuna.value = !!cfg.has_azure_luna
    azureLunaInfo.value = { base: cfg.azure_api_base || '', model: cfg.azure_model || 'gpt-6-luna' }
  } catch {
    status.value = { label: '浏览器预览', ok: null }
  }
}

async function save() {
  const f = form.value
  const r = await wailsCall('SetLLMConfig', f.api_base, f.api_key, f.model, f.fallback_model || '')
  if (r?.success) await load()
  return r
}

async function test() {
  testing.value = true
  testResult.value = null
  try {
    await save()
    testResult.value = await wailsCall('TestLLM')
    status.value = { label: testResult.value?.ok ? 'API 可用' : 'API 异常', ok: testResult.value?.ok ?? false }
  } catch (e: any) {
    testResult.value = { ok: false, message: e?.message }
  } finally {
    testing.value = false
  }
}

async function applyAzureLuna() {
  const res = await wailsCall('LoadAzureLunaConfig')
  if (res?.success) {
    await load()
    await test()
  }
}

export function useLlmSettings() {
  return { form, status, testing, testResult, hasAzureLuna, azureLunaInfo, load, save, test, applyAzureLuna }
}
