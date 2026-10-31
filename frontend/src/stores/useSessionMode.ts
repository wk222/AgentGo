// 会话的 Harness 档位 + 执行画布。后端权威，前端只缓存当前会话的值。
import { ref } from 'vue'
import { wailsCall } from '../shared/wails'
import { notify } from '../shared/ui/notify'

const profile = ref('assistant')
const canvas = ref('balanced')
const saving = ref(false)

function apply(r: any) {
  if (!r) return
  profile.value = r.profile || profile.value
  canvas.value = r.canvas || canvas.value
}

/** 有会话读该会话的模式；没有会话（刚启动）读全局默认。 */
async function load(sessionId: string) {
  try {
    if (sessionId) {
      apply(await wailsCall('GetSessionModeForSession', sessionId))
    } else {
      const flags = await wailsCall('GetFeatureFlags')
      profile.value = flags?.mode_profile || profile.value
      canvas.value = flags?.execution_canvas || canvas.value
    }
  } catch { /* 预览模式或后端暂不可用：保持当前值 */ }
}

async function change(sessionId: string, nextProfile: string, nextCanvas: string) {
  profile.value = nextProfile
  canvas.value = nextCanvas
  saving.value = true
  try {
    apply(sessionId
      ? await wailsCall('SetSessionModeForSession', sessionId, nextProfile, nextCanvas)
      : await wailsCall('SetSessionMode', nextProfile, nextCanvas))
  } catch (e: any) {
    notify.error(`切换模式失败：${e?.message || e}`)
  } finally {
    saving.value = false
  }
}

export function useSessionMode() {
  return { profile, canvas, saving, load, change }
}
