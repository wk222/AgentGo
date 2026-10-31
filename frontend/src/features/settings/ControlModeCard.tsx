import { defineComponent, onMounted, ref } from 'vue'
import { wailsCall } from '../../shared/wails'
import { confirmDialog, notify } from '../../shared/ui/notify'
import type { ControlMode } from '../../shared/ipc/types'

interface ModeOption {
  id: ControlMode
  name: string
  desc: string
}

// 三档与后端 governance.ControlPolicyFromMode 一一对应；文案按它的实际行为写，别写出后端做不到的承诺。
const MODES: ModeOption[] = [
  { id: 'strict', name: '严格', desc: '改文件、运行命令前都要你确认；不能访问工作区以外的文件夹，也不允许智能体创建工具或委派子智能体。适合处理重要资料。' },
  { id: 'balanced', name: '平衡（默认）', desc: '改文件、运行命令、创建或运行自建工具前要你确认；要读写工作区以外的文件夹时也先问。读取和分析不会打扰你。' },
  { id: 'open', name: '开放', desc: '在工作区里改文件不再逐次确认，批量整理表格和文档更省事；运行命令、申请管理员权限、创建工具、访问工作区以外的文件夹等仍会询问。只在信任的工作区使用。' },
]

// 设置 / 协作方式：智能体改动文件、运行命令前要不要先问你。后端把它存进配置并立即换用新策略。
export default defineComponent({
  name: 'ControlModeCard',
  setup() {
    const current = ref<string>('')
    const saving = ref(false)
    const error = ref('')

    onMounted(async () => {
      try {
        const r = await wailsCall('GetGovernancePolicy')
        if (r?.success === false) error.value = String(r.error || '读取操作确认方式失败')
        else current.value = String(r?.control_mode || 'balanced')
      } catch (e) {
        error.value = e instanceof Error ? e.message : String(e)
      }
    })

    const choose = async (mode: ModeOption) => {
      if (mode.id === current.value || saving.value) return
      if (mode.id === 'open') {
        const ok = await confirmDialog('开放模式下，智能体在工作区里改文件不会再逐次问你，改动会直接写入文件，请确保重要资料有备份。\n\n运行命令、申请管理员权限等高风险操作仍会询问。确定切换吗？', {
          title: '切换到开放模式？', okText: '切换', danger: true,
        })
        if (!ok) return
      }
      saving.value = true
      error.value = ''
      try {
        const r = await wailsCall('SetGovernanceControlMode', mode.id)
        if (r?.success === false) throw new Error(String(r.error || '保存失败'))
        current.value = mode.id
        notify.success(`操作确认方式已切换为「${mode.name.replace('（默认）', '')}」，对之后的操作立即生效`)
      } catch (e) {
        error.value = e instanceof Error ? e.message : String(e)
      } finally {
        saving.value = false
      }
    }

    return () => <section class="settings-surface">
      <div class="settings-surface-heading"><div><h3>操作确认方式</h3><p>决定智能体改动文件、运行命令前要不要先问你。切换后对之后的操作立即生效。</p></div></div>
      <div class="settings-mode-list">
        {MODES.map(mode => <button key={mode.id} class={['settings-mode', current.value === mode.id ? 'selected' : '']} disabled={saving.value || !current.value} aria-pressed={current.value === mode.id} onClick={() => void choose(mode)}>
          <span class="settings-mode-radio">{current.value === mode.id && <span />}</span>
          <span><strong>{mode.name}</strong><small>{mode.desc}</small></span>
          {current.value === mode.id && <span class="settings-soft-tag">当前方式</span>}
        </button>)}
      </div>
      {error.value && <div class="settings-feedback error" role="status">{error.value}</div>}
    </section>
  },
})
