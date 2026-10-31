import { computed, defineComponent, onMounted, ref, type PropType } from 'vue'
import { wailsCall } from '../../shared/wails'
import { copyToClipboard } from '../../shared/clipboard'
import { notify } from '../../shared/ui/notify'
import { formatBytes } from '../settings/usageModel'
import type { ArtifactRecord } from '../../shared/ipc/types'
import { artifactBadge, artifactDownloadable, mergeLive } from './artifactModel'
import './artifact.css'

// 同一会话的多张卡共用一次查询（短时缓存），用来知道某个文件后来是否被移除。
const liveCache = new Map<string, { at: number; p: Promise<ArtifactRecord[]> }>()
function loadLive(sessionId: string): Promise<ArtifactRecord[]> {
  const hit = liveCache.get(sessionId)
  if (hit && Date.now() - hit.at < 2000) return hit.p
  const p = wailsCall('ListSessionArtifacts', sessionId).then(r => (r?.artifacts as ArtifactRecord[]) || []).catch(() => [])
  liveCache.set(sessionId, { at: Date.now(), p })
  return p
}

/** 对话里交付的文件卡：下载、复制原路径、从交付物里移除。移除只删交付副本，原文件不动。 */
export default defineComponent({
  name: 'ArtifactCard',
  props: {
    record: { type: Object as PropType<ArtifactRecord>, required: true },
    sessionId: { type: String, default: '' },
  },
  setup(props) {
    const removed = ref(false)
    const busy = ref(false)
    const isRemoved = computed(() => removed.value || props.record.status === 'removed')

    onMounted(async () => {
      if (!props.sessionId || props.record.status === 'removed') return
      const live = await loadLive(props.sessionId)
      if (mergeLive(props.record, live).status === 'removed') removed.value = true
    })

    const copyPath = async () => {
      const ok = await copyToClipboard(props.record.path)
      if (ok) notify.success('已复制路径')
      else notify.error('复制失败')
    }

    const remove = async () => {
      if (busy.value || !props.sessionId) return
      busy.value = true
      try {
        const r = await wailsCall('RemoveArtifact', props.sessionId, props.record.id)
        if (r?.success === false) {
          notify.error(String(r.error || '移除失败'))
          return
        }
        removed.value = true
        liveCache.delete(props.sessionId)
      } catch (e) {
        notify.error(String((e as Error)?.message || e))
      } finally {
        busy.value = false
      }
    }

    return () => {
      const r = props.record
      const gone = isRemoved.value
      return (
        <div class={['artifact-card', gone && 'removed']}>
          <div class="artifact-main">
            <strong class="artifact-name" title={r.path}>{r.title || r.name}</strong>
            <span class="artifact-meta">
              {r.title && r.title !== r.name ? `${r.name} · ` : ''}
              {formatBytes(r.size)} · {gone ? '已移除' : artifactBadge(r)}
            </span>
          </div>
          <div class="artifact-actions">
            {!gone && artifactDownloadable(r) && (
              <a class="artifact-btn primary" href={r.url} download={r.name} target="_blank" rel="noopener">下载</a>
            )}
            <button type="button" class="artifact-btn" onClick={copyPath} title={r.path}>复制路径</button>
            {!gone && props.sessionId && (
              <button type="button" class="artifact-btn danger" disabled={busy.value} onClick={remove}>移除</button>
            )}
          </div>
        </div>
      )
    }
  },
})
