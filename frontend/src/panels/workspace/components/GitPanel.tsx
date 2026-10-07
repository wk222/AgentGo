import { defineComponent, ref, onMounted, type PropType } from 'vue'
import { wailsCall } from '../../../wails'
import type { GitStatusItem } from '../types'
import { FileIcon, GitStatusBadge } from '../utils/icons'

export const GitPanel = defineComponent({
  name: 'GitPanel',
  props: {
    activePath: { type: String, default: '' },
  },
  emits: ['openDiff', 'selectFile', 'branchChanged'],
  setup(props, { emit }) {
    const branch = ref('main')
    const branches = ref<string[]>([])
    const staged = ref<GitStatusItem[]>([])
    const unstaged = ref<GitStatusItem[]>([])
    const untracked = ref<GitStatusItem[]>([])
    const commitMessage = ref('')
    const loading = ref(false)
    const committing = ref(false)
    const errorMsg = ref('')

    const loadGitStatus = async () => {
      loading.value = true
      errorMsg.value = ''
      try {
        const res = await wailsCall<{
          success?: boolean
          branch?: string
          staged?: GitStatusItem[]
          unstaged?: GitStatusItem[]
          untracked?: GitStatusItem[]
          error?: string
        }>('WorkspaceGitStatus')

        if (res?.error) {
          errorMsg.value = res.error
          return
        }

        branch.value = res?.branch || 'main'
        staged.value = res?.staged || []
        unstaged.value = res?.unstaged || []
        untracked.value = res?.untracked || []
      } catch (e: any) {
        errorMsg.value = e?.message || '获取 Git 状态失败'
      } finally {
        loading.value = false
      }
    }

    const loadBranches = async () => {
      try {
        const res = await wailsCall<{
          success?: boolean
          branches?: string[]
          current?: string
        }>('WorkspaceGitBranches')
        if (res?.branches) {
          branches.value = res.branches
        }
      } catch {
        // ignore
      }
    }

    const stageFile = async (filePath: string) => {
      try {
        await wailsCall('WorkspaceGitStage', filePath)
        loadGitStatus()
      } catch (e: any) {
        alert(`Stage 失败: ${e?.message || e}`)
      }
    }

    const stageAll = async () => {
      try {
        await wailsCall('WorkspaceGitStage', '')
        loadGitStatus()
      } catch (e: any) {
        alert(`Stage All 失败: ${e?.message || e}`)
      }
    }

    const unstageFile = async (filePath: string) => {
      try {
        await wailsCall('WorkspaceGitUnstage', filePath)
        loadGitStatus()
      } catch (e: any) {
        alert(`Unstage 失败: ${e?.message || e}`)
      }
    }

    const unstageAll = async () => {
      try {
        await wailsCall('WorkspaceGitUnstage', '')
        loadGitStatus()
      } catch (e: any) {
        alert(`Unstage All 失败: ${e?.message || e}`)
      }
    }

    const commitChanges = async () => {
      if (!commitMessage.value.trim()) {
        alert('请输入提交信息 (Commit Message)')
        return
      }

      committing.value = true
      try {
        const res = await wailsCall<{ success?: boolean; error?: string; output?: string }>(
          'WorkspaceGitCommit',
          commitMessage.value.trim()
        )
        if (res?.error) {
          alert(`Commit 失败: ${res.error}`)
          return
        }

        commitMessage.value = ''
        loadGitStatus()
      } catch (e: any) {
        alert(`Commit 失败: ${e?.message || e}`)
      } finally {
        committing.value = false
      }
    }

    onMounted(() => {
      loadGitStatus()
      loadBranches()
    })

    return {
      branch,
      branches,
      staged,
      unstaged,
      untracked,
      commitMessage,
      loading,
      committing,
      errorMsg,
      loadGitStatus,
      stageFile,
      stageAll,
      unstageFile,
      unstageAll,
      commitChanges,
      onOpenDiff: (item: GitStatusItem) => {
        emit('openDiff', item)
      },
      onSelectFile: (item: GitStatusItem) => {
        emit('selectFile', item)
      },
    }
  },
  render() {
    return (
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          height: '100%',
          backgroundColor: 'var(--sidebar, #18181B)',
          color: 'var(--text, #D4D4D8)',
          fontSize: '12px',
          overflow: 'hidden',
        }}
      >
        {/* Panel Header */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '8px 12px 6px',
            fontSize: '11px',
            fontWeight: 700,
            textTransform: 'uppercase',
            letterSpacing: '0.5px',
            color: 'var(--text-dim)',
            borderBottom: '1px solid var(--border-subtle)',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
            <span>源代码管理 (GIT)</span>
            <span
              style={{
                fontSize: '10px',
                padding: '1px 6px',
                borderRadius: '999px',
                backgroundColor: 'var(--accent-bg)',
                color: 'var(--accent)',
                fontWeight: 600,
              }}
            >
              {this.branch}
            </span>
          </div>
          <button
            onClick={this.loadGitStatus}
            disabled={this.loading}
            title="刷新 Git 状态"
            style={{
              background: 'none',
              border: 'none',
              color: 'var(--text-dim)',
              cursor: 'pointer',
              fontSize: '13px',
              padding: '2px 4px',
            }}
          >
            ↻
          </button>
        </div>

        {/* Commit Input Area */}
        <div style={{ padding: '10px 12px', borderBottom: '1px solid var(--border-subtle)' }}>
          <textarea
            value={this.commitMessage}
            onInput={(e) => { this.commitMessage = (e.target as HTMLTextAreaElement).value }}
            onKeydown={(e) => {
              if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
                this.commitChanges()
              }
            }}
            placeholder="输入提交信息 (Ctrl+Enter 提交)..."
            rows={3}
            style={{
              width: '100%',
              backgroundColor: 'var(--input-bg)',
              border: '1px solid var(--border)',
              borderRadius: 'var(--radius-sm)',
              color: 'var(--text-strong)',
              fontSize: '12px',
              fontFamily: 'var(--font-ui, sans-serif)',
              padding: '6px 8px',
              boxSizing: 'border-box',
              resize: 'none',
              outline: 'none',
            }}
          />
          <button
            onClick={this.commitChanges}
            disabled={this.committing || (this.staged.length === 0 && this.unstaged.length === 0)}
            style={{
              width: '100%',
              marginTop: '6px',
              padding: '6px 0',
              backgroundColor: 'var(--accent)',
              color: '#ffffff',
              fontWeight: 600,
              fontSize: '12px',
              border: 'none',
              borderRadius: 'var(--radius-sm)',
              cursor: 'pointer',
              opacity: this.staged.length === 0 && this.unstaged.length === 0 ? 0.6 : 1,
              transition: 'background 0.15s ease',
            }}
          >
            {this.committing ? '正在提交...' : `✓ 提交 (${this.staged.length} 暂存)`}
          </button>
        </div>

        {/* Changes Lists */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '6px 0' }}>
          {this.errorMsg ? (
            <div style={{ padding: '16px', color: 'var(--error)', fontSize: '12px' }}>
              {this.errorMsg}
            </div>
          ) : (
            <div>
              {/* Staged Changes */}
              <div>
                <div
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    padding: '4px 12px',
                    fontSize: '11px',
                    fontWeight: 600,
                    color: 'var(--text-dim)',
                  }}
                >
                  <span>已暂存更改 ({this.staged.length})</span>
                  {this.staged.length > 0 && (
                    <button
                      onClick={this.unstageAll}
                      title="取消暂存所有更改"
                      style={{
                        background: 'none',
                        border: 'none',
                        color: 'var(--text-dim)',
                        cursor: 'pointer',
                        fontSize: '12px',
                      }}
                    >
                      − 全部取消
                    </button>
                  )}
                </div>
                {this.staged.map((item) => (
                  <div
                    key={`staged_${item.path}`}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: '6px',
                      padding: '4px 12px 4px 16px',
                      fontSize: '12px',
                      cursor: 'pointer',
                    }}
                    onMouseenter={(e) => {
                      (e.currentTarget as HTMLElement).style.backgroundColor = 'var(--surface-hover)'
                    }}
                    onMouseleave={(e) => {
                      (e.currentTarget as HTMLElement).style.backgroundColor = 'transparent'
                    }}
                  >
                    <FileIcon fileName={item.name} isDir={false} size={14} />
                    <span
                      onClick={() => this.onOpenDiff(item)}
                      style={{
                        flex: 1,
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                        color: 'var(--text)',
                      }}
                    >
                      {item.name}
                    </span>
                    <GitStatusBadge status={item.status} />
                    <button
                      onClick={() => this.unstageFile(item.path)}
                      title="取消暂存"
                      style={{
                        background: 'none',
                        border: 'none',
                        color: 'var(--text-dim)',
                        cursor: 'pointer',
                        fontSize: '12px',
                        padding: '0 4px',
                      }}
                    >
                      −
                    </button>
                  </div>
                ))}
              </div>

              {/* Unstaged Changes */}
              <div style={{ marginTop: '10px' }}>
                <div
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    padding: '4px 12px',
                    fontSize: '11px',
                    fontWeight: 600,
                    color: 'var(--text-dim)',
                  }}
                >
                  <span>更改 ({this.unstaged.length + this.untracked.length})</span>
                  {this.unstaged.length + this.untracked.length > 0 && (
                    <button
                      onClick={this.stageAll}
                      title="暂存所有更改"
                      style={{
                        background: 'none',
                        border: 'none',
                        color: 'var(--accent)',
                        cursor: 'pointer',
                        fontSize: '12px',
                      }}
                    >
                      + 全部暂存
                    </button>
                  )}
                </div>
                {[...this.unstaged, ...this.untracked].map((item) => (
                  <div
                    key={`unstaged_${item.path}`}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: '6px',
                      padding: '4px 12px 4px 16px',
                      fontSize: '12px',
                      cursor: 'pointer',
                    }}
                    onMouseenter={(e) => {
                      (e.currentTarget as HTMLElement).style.backgroundColor = 'var(--surface-hover)'
                    }}
                    onMouseleave={(e) => {
                      (e.currentTarget as HTMLElement).style.backgroundColor = 'transparent'
                    }}
                  >
                    <FileIcon fileName={item.name} isDir={false} size={14} />
                    <span
                      onClick={() => this.onOpenDiff(item)}
                      style={{
                        flex: 1,
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                        color: 'var(--text)',
                      }}
                    >
                      {item.name}
                    </span>
                    <GitStatusBadge status={item.status} />
                    <button
                      onClick={() => this.stageFile(item.path)}
                      title="暂存更改"
                      style={{
                        background: 'none',
                        border: 'none',
                        color: 'var(--text-dim)',
                        cursor: 'pointer',
                        fontSize: '12px',
                        padding: '0 4px',
                      }}
                    >
                      +
                    </button>
                  </div>
                ))}
              </div>

              {this.staged.length === 0 && this.unstaged.length === 0 && this.untracked.length === 0 && (
                <div style={{ padding: '32px 16px', textAlign: 'center', color: 'var(--text-dim)', fontSize: '12px' }}>
                  工作树整洁，无未提交的更改
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    )
  },
})
