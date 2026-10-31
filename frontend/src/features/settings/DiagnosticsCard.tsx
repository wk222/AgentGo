import { defineComponent, onMounted, ref } from 'vue'
import { Check, Copy, Download, LifeBuoy, LoaderCircle } from 'lucide-vue-next'
import { wailsCall, wailsCallWithTimeout } from '../../shared/wails'
import type { AppInfo, DiagnosticsExport } from '../../shared/ipc/types'
import { formatBytes } from './usageModel'
import './usage-diagnostics.css'

const INCLUDED = ['版本、系统与运行方式', '模型连接：地址和模型名（不含密钥）', '运行参数（密钥已隐藏）与功能开关', '插件与工具清单、近 14 天模型用量', '数据库各表的行数与完整性检查', '日志末尾（密钥与用户名已隐藏）', '上次异常退出时的现场记录（如果有）']
const EXCLUDED = ['对话内容与附件', '记忆内容', '工作区里的文件', 'API 密钥与访问令牌']

const hostName = (host?: string) => (host === 'web' ? '浏览器版' : host === 'desktop' ? '桌面版' : host || '未知')
const crashTime = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '时间未知' : d.toLocaleString('zh-CN', { hour12: false })
}

// 设置 / 诊断包：打不开、连不上模型、结果异常时，一键生成一个压缩包发给维护人员。
export default defineComponent({
  name: 'DiagnosticsCard',
  setup() {
    const info = ref<AppInfo | null>(null)
    const busy = ref(false)
    const error = ref('')
    const result = ref<DiagnosticsExport | null>(null)
    const copied = ref(false)

    onMounted(async () => {
      try { info.value = await wailsCall('GetAppInfo') } catch { /* 版本只是辅助信息，读不到不影响生成诊断包 */ }
    })

    const make = async () => {
      busy.value = true
      error.value = ''
      result.value = null
      copied.value = false
      try {
        const res = await wailsCallWithTimeout('ExportDiagnostics', 45000)
        if (res?.success === false || !res?.url) error.value = String(res?.error || '生成诊断包失败')
        else result.value = res as unknown as DiagnosticsExport
      } catch (e) {
        error.value = e instanceof Error ? e.message : String(e)
      } finally {
        busy.value = false
      }
    }

    const copyPath = async () => {
      if (!result.value) return
      try {
        await navigator.clipboard.writeText(result.value.path)
        copied.value = true
      } catch {
        copied.value = false
        error.value = '无法写入剪贴板，请手动选中路径复制。'
      }
    }

    const row = (label: string, value?: string) => value ? <div><dt>{label}</dt><dd title={value}>{value}</dd></div> : null

    return () => {
      const i = info.value
      const r = result.value
      return <section class="settings-surface diag-card">
        <div class="settings-surface-heading"><div><h3>诊断包</h3><p>遇到打不开、模型连不上、结果异常时，生成一个压缩包发给维护人员，比描述现象更快定位问题。</p></div></div>
        {i && <dl class="diag-build">
          {row('版本', i.version)}
          {row('运行方式', hostName(i.host))}
          {row('系统', [i.os === 'windows' ? 'Windows' : i.os, i.os_version, i.arch].filter(Boolean).join(' · '))}
          {row('数据目录', i.data_dir)}
        </dl>}
        {i?.previous_crash && <div class="settings-feedback error" role="status">
          <strong>上次运行异常退出</strong>（{crashTime(i.previous_crash.time)}）：{i.previous_crash.headline || '原因未知'}。建议现在生成诊断包发给维护人员，里面带有当时的现场记录。
        </div>}
        <div class="diag-actions">
          <button class="settings-primary" disabled={busy.value} onClick={make}>{busy.value ? <LoaderCircle size={14} class="settings-spin" /> : <LifeBuoy size={14} />}{busy.value ? '正在生成…' : '生成诊断包'}</button>
          <span>通常几秒钟，最多保留最近 3 个。</span>
        </div>
        {error.value && <div class="settings-feedback error" role="status">{error.value}</div>}
        {r && <div class="diag-result" role="status">
          <div class="diag-result-head"><Check size={16} /><strong>{r.name}</strong><span>{formatBytes(r.size)}</span></div>
          <div class="diag-result-actions">
            <a class="settings-primary" href={r.url} download={r.name}><Download size={14} />下载诊断包</a>
            <button class="settings-secondary" onClick={copyPath}>{copied.value ? <Check size={14} /> : <Copy size={14} />}{copied.value ? '已复制路径' : '复制文件路径'}</button>
          </div>
          <code class="diag-path" title={r.path}>{r.path}</code>
        </div>}
        <div class="diag-scope">
          <div><strong>包含</strong><ul>{INCLUDED.map(t => <li key={t}>{t}</li>)}</ul></div>
          <div><strong>不含</strong><ul>{EXCLUDED.map(t => <li key={t}>{t}</li>)}</ul></div>
        </div>
      </section>
    }
  },
})
