import { defineComponent, onMounted, onUnmounted, ref } from 'vue'
import { wailsCall } from '../../../wails'
import type { WorkspaceReviewChange } from '../types'
import { FileIcon } from '../utils/icons'

export const ReviewPanel = defineComponent({
  name: 'ReviewPanel',
  props: {
    sessionId: { type: String, default: '' },
  },
  emits: ['openDiff', 'fileRestored', 'fileAccepted', 'reviewCompleted'],
  setup(props, { emit }) {
    const changes = ref<WorkspaceReviewChange[]>([])
    const active = ref(false)
    const loading = ref(false)
    const busyPath = ref('')
    const errorMsg = ref('')
    const skippedCount = ref(0)
    let timer: number | undefined

    const load = async () => {
      loading.value = true
      try {
        const res = await wailsCall<{
          success?: boolean
          active?: boolean
          changes?: WorkspaceReviewChange[]
          skipped_count?: number
          error?: string
        }>('ListWorkspaceReview', props.sessionId)
        if (res?.success === false || res?.error) {
          errorMsg.value = res?.error || '读取 AI 变更失败'
          return
        }
        errorMsg.value = ''
        active.value = !!res?.active
        changes.value = res?.changes || []
        skippedCount.value = Number(res?.skipped_count || 0)
      } catch (e: any) {
        errorMsg.value = e?.message || '读取 AI 变更失败'
      } finally {
        loading.value = false
      }
    }

    const openDiff = async (change: WorkspaceReviewChange) => {
      if (change.binary) {
        alert('二进制文件不能文本预览，但仍可接受或恢复。')
        return
      }
      try {
        const res = await wailsCall<{
          success?: boolean
          error?: string
          original_content?: string
          modified_content?: string
        }>('WorkspaceReviewFile', props.sessionId, change.path)
        if (res?.success === false || res?.error) {
          alert(res?.error || '读取差异失败')
          return
        }
        emit('openDiff', {
          path: change.path,
          originalContent: res?.original_content || '',
          modifiedContent: res?.modified_content || '',
        })
      } catch (e: any) {
        alert(e?.message || '读取差异失败')
      }
    }

    const accept = async (change: WorkspaceReviewChange) => {
      busyPath.value = change.path
      try {
        const res = await wailsCall<{ success?: boolean; error?: string }>(
          'AcceptWorkspaceReviewFile', props.sessionId, change.path
        )
        if (res?.success === false || res?.error) alert(res?.error || '接受失败')
        else emit('fileAccepted', change.path)
        await load()
      } finally {
        busyPath.value = ''
      }
    }

    const reject = async (change: WorkspaceReviewChange) => {
      if (!change.reviewable) {
        alert(change.reason || '这个文件没有安全快照，不能自动恢复。')
        return
      }
      if (!confirm(`恢复 ${change.path} 到本次 AI 修改前的状态？`)) return
      busyPath.value = change.path
      try {
        const res = await wailsCall<{ success?: boolean; error?: string }>(
          'RejectWorkspaceReviewFile', props.sessionId, change.path
        )
        if (res?.success === false || res?.error) {
          alert(res?.error || '恢复失败')
        } else {
          emit('fileRestored', change.path)
        }
        await load()
      } finally {
        busyPath.value = ''
      }
    }

    const acceptAll = async () => {
      await wailsCall('AcceptAllWorkspaceReview', props.sessionId)
      emit('reviewCompleted')
      await load()
    }

    const rejectAll = async () => {
      if (!changes.value.length) return
      if (!confirm(`恢复全部 ${changes.value.length} 个文件到本次 AI 修改前？`)) return
      const res = await wailsCall<{ success?: boolean; error?: string }>('RejectAllWorkspaceReview', props.sessionId)
      if (res?.success === false || res?.error) {
        alert(res?.error || '全部恢复失败')
      } else {
        for (const change of changes.value) emit('fileRestored', change.path)
        emit('reviewCompleted')
      }
      await load()
    }

    onMounted(() => {
      void load()
      timer = window.setInterval(load, 2000)
    })
    onUnmounted(() => {
      if (timer !== undefined) window.clearInterval(timer)
    })

    return { changes, active, loading, busyPath, errorMsg, skippedCount, load, openDiff, accept, reject, acceptAll, rejectAll }
  },
  render() {
    const buttonStyle = (primary = false) => ({
      border: `1px solid ${primary ? '#38BDF8' : 'rgba(255,255,255,0.13)'}`,
      borderRadius: '4px',
      background: primary ? 'rgba(56,189,248,0.14)' : 'transparent',
      color: primary ? '#7DD3FC' : '#A1A1AA',
      cursor: 'pointer',
      fontSize: '11px',
      padding: '3px 7px',
    })
    const statusLabel: Record<string, string> = { modified: 'M', added: 'A', deleted: 'D' }
    const statusColor: Record<string, string> = { modified: '#FBBF24', added: '#4ADE80', deleted: '#F87171' }
    return (
      <div style={{ height: '100%', display: 'flex', flexDirection: 'column', color: '#D4D4D8', fontSize: '12px' }}>
        <div style={{ padding: '9px 10px', borderBottom: '1px solid rgba(255,255,255,0.07)' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <strong style={{ fontSize: '11px', letterSpacing: '0.5px' }}>AI 变更审查</strong>
            <button onClick={this.load} style={buttonStyle()}>↻</button>
          </div>
          <div style={{ color: '#71717A', fontSize: '11px', lineHeight: 1.5, marginTop: '6px' }}>
            “拒绝”会恢复到这次 AI 开始前，不会回退你更早的本地修改。
          </div>
          {this.changes.length > 0 && (
            <div style={{ display: 'flex', gap: '6px', marginTop: '9px' }}>
              <button onClick={this.acceptAll} style={buttonStyle(true)}>全部接受</button>
              <button onClick={this.rejectAll} style={buttonStyle()}>全部恢复</button>
            </div>
          )}
        </div>
        <div style={{ flex: 1, overflowY: 'auto', padding: '6px 0' }}>
          {this.errorMsg ? (
            <div style={{ padding: '14px', color: '#F87171' }}>{this.errorMsg}</div>
          ) : this.changes.length === 0 ? (
            <div style={{ padding: '28px 14px', textAlign: 'center', color: '#71717A', lineHeight: 1.6 }}>
              {this.loading ? '检查变更中…' : this.active ? 'Agent 尚未产生文件变更' : '暂无待审查的 AI 变更'}
            </div>
          ) : this.changes.map((change) => (
            <div key={change.path} style={{ padding: '7px 10px', borderBottom: '1px solid rgba(255,255,255,0.04)' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                <FileIcon fileName={change.name} isDir={false} size={14} />
                <button
                  onClick={() => this.openDiff(change)}
                  title={change.path}
                  style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', textAlign: 'left', border: 'none', background: 'none', color: '#E4E4E7', cursor: 'pointer', padding: 0 }}
                >
                  {change.name}
                </button>
                <span style={{ color: statusColor[change.status] || '#A1A1AA', fontWeight: 700 }}>{statusLabel[change.status] || '?'}</span>
              </div>
              <div style={{ color: '#52525B', fontSize: '10px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', margin: '3px 0 6px 20px' }}>{change.path}</div>
              <div style={{ display: 'flex', gap: '5px', marginLeft: '20px' }}>
                <button disabled={this.busyPath === change.path} onClick={() => this.accept(change)} style={buttonStyle(true)}>接受</button>
                <button disabled={this.busyPath === change.path || !change.reviewable} onClick={() => this.reject(change)} title={change.reason || '恢复到 AI 修改前'} style={{ ...buttonStyle(), opacity: change.reviewable ? 1 : 0.45 }}>恢复</button>
              </div>
            </div>
          ))}
          {this.skippedCount > 0 && (
            <div style={{ padding: '10px', color: '#A16207', fontSize: '10px', lineHeight: 1.5 }}>
              {this.skippedCount} 个超大或特殊文件只记录了指纹；若被修改，可能无法自动恢复。
            </div>
          )}
        </div>
      </div>
    )
  },
})
