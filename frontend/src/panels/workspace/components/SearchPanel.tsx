import { defineComponent, ref, reactive, computed, type PropType } from 'vue'
import { wailsCall } from '../../../wails'
import type { FileSearchResult, SearchOptions, SearchMatch } from '../types'
import { FileIcon } from '../utils/icons'

export const SearchPanel = defineComponent({
  name: 'SearchPanel',
  props: {
    activePath: { type: String, default: '' },
  },
  emits: ['openMatch', 'replaceFileContent'],
  setup(props, { emit }) {
    const isExpanded = ref(true)
    const showReplace = ref(false)
    const showDetails = ref(false)
    const loading = ref(false)
    const errorMsg = ref('')

    const options = reactive<SearchOptions>({
      query: '',
      replaceText: '',
      isRegex: false,
      matchCase: false,
      matchWholeWord: false,
      includePattern: '',
      excludePattern: 'node_modules, dist, .git',
    })

    const results = ref<FileSearchResult[]>([])
    const totalMatches = ref(0)
    const totalFiles = ref(0)
    const collapsedFiles = ref<Set<string>>(new Set())

    const performSearch = async () => {
      if (!options.query.trim()) {
        results.value = []
        totalMatches.value = 0
        totalFiles.value = 0
        return
      }

      loading.value = true
      errorMsg.value = ''
      try {
        const res = await wailsCall<{
          success?: boolean
          results?: FileSearchResult[]
          totalMatches?: number
          totalFiles?: number
          error?: string
        }>(
          'WorkspaceSearch',
          options.query,
          options.isRegex,
          options.matchCase,
          options.matchWholeWord,
          options.includePattern,
          options.excludePattern,
          200
        )

        if (res?.error) {
          errorMsg.value = res.error
          results.value = []
          totalMatches.value = 0
          totalFiles.value = 0
          return
        }

        results.value = res?.results || []
        totalMatches.value = res?.totalMatches || 0
        totalFiles.value = res?.totalFiles || 0
        collapsedFiles.value.clear()
      } catch (e: any) {
        errorMsg.value = e?.message || '搜索执行失败'
      } finally {
        loading.value = false
      }
    }

    const toggleFileCollapse = (filePath: string) => {
      if (collapsedFiles.value.has(filePath)) {
        collapsedFiles.value.delete(filePath)
      } else {
        collapsedFiles.value.add(filePath)
      }
    }

    const replaceAllInFile = async (fileResult: FileSearchResult) => {
      try {
        const readRes = await wailsCall<{ content?: string; error?: string }>('ReadWorkspaceFile', fileResult.filePath)
        if (readRes?.error || readRes?.content == null) {
          alert(`读取文件失败: ${readRes?.error}`)
          return
        }

        let content = readRes.content
        let regex: RegExp
        let flags = options.matchCase ? 'g' : 'gi'

        if (options.isRegex) {
          regex = new RegExp(options.query, flags)
        } else {
          const escaped = options.query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
          const pattern = options.matchWholeWord ? `\\b${escaped}\\b` : escaped
          regex = new RegExp(pattern, flags)
        }

        const newContent = content.replace(regex, options.replaceText)
        const writeRes = await wailsCall<{ error?: string }>('WriteWorkspaceFile', fileResult.filePath, newContent)
        if (writeRes?.error) {
          alert(`写入文件失败: ${writeRes.error}`)
          return
        }

        emit('replaceFileContent', fileResult.filePath, newContent)
        performSearch()
      } catch (e: any) {
        alert(`替换失败: ${e?.message || e}`)
      }
    }

    const replaceAllGlobal = async () => {
      if (!confirm(`确定要在 ${totalFiles.value} 个文件中替换全部 ${totalMatches.value} 处匹配项吗？`)) {
        return
      }

      for (const fileResult of results.value) {
        await replaceAllInFile(fileResult)
      }
    }

    return {
      isExpanded,
      showReplace,
      showDetails,
      loading,
      errorMsg,
      options,
      results,
      totalMatches,
      totalFiles,
      collapsedFiles,
      performSearch,
      toggleFileCollapse,
      replaceAllInFile,
      replaceAllGlobal,
      onOpenMatch: (filePath: string, match: SearchMatch) => {
        emit('openMatch', {
          filePath,
          line: match.line,
          column: match.column,
        })
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
            color: 'var(--text-dim, #71717A)',
            borderBottom: '1px solid var(--border-subtle, rgba(255,255,255,0.06))',
          }}
        >
          <span>全局搜索与替换</span>
          <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
            <button
              onClick={() => { this.showReplace = !this.showReplace }}
              title={this.showReplace ? '隐藏替换' : '展开替换'}
              style={{
                background: this.showReplace ? 'rgba(56, 189, 248, 0.2)' : 'none',
                color: this.showReplace ? '#38BDF8' : '#A1A1AA',
                border: 'none',
                borderRadius: '3px',
                padding: '2px 4px',
                cursor: 'pointer',
                fontSize: '11px',
              }}
            >
              ⇄
            </button>
            <button
              onClick={() => { this.showDetails = !this.showDetails }}
              title={this.showDetails ? '隐藏过滤选项' : '高级文件过滤'}
              style={{
                background: this.showDetails ? 'rgba(56, 189, 248, 0.2)' : 'none',
                color: this.showDetails ? '#38BDF8' : '#A1A1AA',
                border: 'none',
                borderRadius: '3px',
                padding: '2px 4px',
                cursor: 'pointer',
                fontSize: '11px',
              }}
            >
              ⚙
            </button>
          </div>
        </div>

        {/* Inputs Area */}
        <div style={{ padding: '10px 12px', display: 'flex', flexDirection: 'column', gap: '6px', borderBottom: '1px solid rgba(255,255,255,0.06)' }}>
          {/* Search Query Input + Toggle Options */}
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              backgroundColor: '#27272A',
              border: '1px solid rgba(255, 255, 255, 0.1)',
              borderRadius: '4px',
              padding: '2px 4px',
            }}
          >
            <input
              type="text"
              value={this.options.query}
              onInput={(e) => { this.options.query = (e.target as HTMLInputElement).value }}
              onKeydown={(e) => { if (e.key === 'Enter') this.performSearch() }}
              placeholder="搜索文本 (Enter 执行)..."
              style={{
                flex: 1,
                background: 'transparent',
                border: 'none',
                outline: 'none',
                color: '#FFFFFF',
                fontSize: '12px',
                padding: '3px 4px',
              }}
            />
            {/* Toggle matchCase */}
            <button
              onClick={() => { this.options.matchCase = !this.options.matchCase }}
              title="区分大小写 (Match Case)"
              style={{
                background: this.options.matchCase ? 'var(--accent, #38BDF8)' : 'transparent',
                color: this.options.matchCase ? '#09090B' : '#A1A1AA',
                border: 'none',
                borderRadius: '2px',
                padding: '1px 4px',
                cursor: 'pointer',
                fontSize: '10px',
                fontWeight: 700,
              }}
            >
              Aa
            </button>
            {/* Toggle matchWholeWord */}
            <button
              onClick={() => { this.options.matchWholeWord = !this.options.matchWholeWord }}
              title="全字匹配 (Match Whole Word)"
              style={{
                background: this.options.matchWholeWord ? 'var(--accent, #38BDF8)' : 'transparent',
                color: this.options.matchWholeWord ? '#09090B' : '#A1A1AA',
                border: 'none',
                borderRadius: '2px',
                padding: '1px 4px',
                cursor: 'pointer',
                fontSize: '10px',
                fontWeight: 700,
              }}
            >
              \b
            </button>
            {/* Toggle isRegex */}
            <button
              onClick={() => { this.options.isRegex = !this.options.isRegex }}
              title="正则表达式 (Regular Expression)"
              style={{
                background: this.options.isRegex ? 'var(--accent, #38BDF8)' : 'transparent',
                color: this.options.isRegex ? '#09090B' : '#A1A1AA',
                border: 'none',
                borderRadius: '2px',
                padding: '1px 4px',
                cursor: 'pointer',
                fontSize: '10px',
                fontWeight: 700,
              }}
            >
              .*
            </button>
          </div>

          {/* Replace Input */}
          {this.showReplace && (
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                backgroundColor: '#27272A',
                border: '1px solid rgba(255, 255, 255, 0.1)',
                borderRadius: '4px',
                padding: '2px 4px',
              }}
            >
              <input
                type="text"
                value={this.options.replaceText}
                onInput={(e) => { this.options.replaceText = (e.target as HTMLInputElement).value }}
                placeholder="替换为..."
                style={{
                  flex: 1,
                  background: 'transparent',
                  border: 'none',
                  outline: 'none',
                  color: '#FFFFFF',
                  fontSize: '12px',
                  padding: '3px 4px',
                }}
              />
              <button
                onClick={this.replaceAllGlobal}
                disabled={this.totalMatches === 0}
                title="全部替换"
                style={{
                  background: this.totalMatches > 0 ? 'rgba(56, 189, 248, 0.2)' : 'transparent',
                  color: this.totalMatches > 0 ? '#38BDF8' : '#71717A',
                  border: 'none',
                  borderRadius: '2px',
                  padding: '2px 6px',
                  cursor: this.totalMatches > 0 ? 'pointer' : 'default',
                  fontSize: '11px',
                  fontWeight: 600,
                }}
              >
                全部替换
              </button>
            </div>
          )}

          {/* Advanced Filters */}
          {this.showDetails && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '4px', marginTop: '4px' }}>
              <div>
                <span style={{ fontSize: '10px', color: '#71717A' }}>包含的文件 (如 *.ts, *.go):</span>
                <input
                  type="text"
                  value={this.options.includePattern}
                  onInput={(e) => { this.options.includePattern = (e.target as HTMLInputElement).value }}
                  placeholder="如: *.ts, *.tsx"
                  style={{
                    width: '100%',
                    backgroundColor: '#27272A',
                    border: '1px solid rgba(255,255,255,0.08)',
                    borderRadius: '3px',
                    color: '#D4D4D8',
                    fontSize: '11px',
                    padding: '2px 6px',
                    boxSizing: 'border-box',
                    marginTop: '2px',
                  }}
                />
              </div>
              <div>
                <span style={{ fontSize: '10px', color: '#71717A' }}>排除的文件/目录:</span>
                <input
                  type="text"
                  value={this.options.excludePattern}
                  onInput={(e) => { this.options.excludePattern = (e.target as HTMLInputElement).value }}
                  placeholder="如: node_modules, dist, .git"
                  style={{
                    width: '100%',
                    backgroundColor: '#27272A',
                    border: '1px solid rgba(255,255,255,0.08)',
                    borderRadius: '3px',
                    color: '#D4D4D8',
                    fontSize: '11px',
                    padding: '2px 6px',
                    boxSizing: 'border-box',
                    marginTop: '2px',
                  }}
                />
              </div>
            </div>
          )}

          {/* Search Trigger Button */}
          <button
            onClick={this.performSearch}
            disabled={this.loading}
            style={{
              padding: '4px 8px',
              backgroundColor: 'var(--accent, #38BDF8)',
              color: '#09090B',
              fontWeight: 600,
              border: 'none',
              borderRadius: '4px',
              cursor: 'pointer',
              fontSize: '11px',
              marginTop: '2px',
            }}
          >
            {this.loading ? '正在检索...' : '立即搜索'}
          </button>
        </div>

        {/* Search Results Summary */}
        {this.totalMatches > 0 && (
          <div
            style={{
              padding: '6px 12px',
              fontSize: '11px',
              color: '#A1A1AA',
              backgroundColor: 'rgba(255,255,255,0.02)',
              borderBottom: '1px solid rgba(255,255,255,0.04)',
              display: 'flex',
              justifyContent: 'space-between',
            }}
          >
            <span>共找到 {this.totalMatches} 个结果（在 {this.totalFiles} 个文件中）</span>
          </div>
        )}

        {/* Results List */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '4px 0' }}>
          {this.errorMsg ? (
            <div style={{ padding: '16px', color: '#EF4444', fontSize: '12px' }}>
              {this.errorMsg}
            </div>
          ) : this.results.length === 0 ? (
            <div style={{ padding: '32px 16px', textAlign: 'center', color: '#71717A', fontSize: '12px' }}>
              {this.loading ? '搜索中...' : '无搜索结果'}
            </div>
          ) : (
            this.results.map((fileRes) => {
              const isCollapsed = this.collapsedFiles.has(fileRes.filePath)
              return (
                <div key={fileRes.filePath} style={{ marginBottom: '4px' }}>
                  {/* File Header */}
                  <div
                    onClick={() => this.toggleFileCollapse(fileRes.filePath)}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: '6px',
                      padding: '4px 10px',
                      backgroundColor: 'rgba(255,255,255,0.03)',
                      cursor: 'pointer',
                      fontSize: '12px',
                      userSelect: 'none',
                    }}
                  >
                    <span
                      style={{
                        transform: isCollapsed ? 'rotate(0deg)' : 'rotate(90deg)',
                        transition: 'transform 0.15s ease',
                        fontSize: '10px',
                        color: '#71717A',
                      }}
                    >
                      ▶
                    </span>
                    <FileIcon fileName={fileRes.fileName} isDir={false} size={14} />
                    <span style={{ fontWeight: 600, color: '#FFFFFF' }}>{fileRes.fileName}</span>
                    <span style={{ fontSize: '11px', color: '#71717A', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1 }}>
                      {fileRes.filePath}
                    </span>
                    <span
                      style={{
                        fontSize: '10px',
                        padding: '1px 5px',
                        borderRadius: '999px',
                        backgroundColor: 'rgba(56, 189, 248, 0.2)',
                        color: '#38BDF8',
                        fontWeight: 600,
                      }}
                    >
                      {fileRes.matches.length}
                    </span>
                  </div>

                  {/* Matches Lines */}
                  {!isCollapsed && (
                    <div>
                      {fileRes.matches.map((m, idx) => (
                        <div
                          key={`${fileRes.filePath}_${m.line}_${m.column}_${idx}`}
                          onClick={() => this.onOpenMatch(fileRes.filePath, m)}
                          style={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: '8px',
                            padding: '3px 12px 3px 28px',
                            cursor: 'pointer',
                            fontSize: '11px',
                            fontFamily: 'monospace',
                            color: '#D4D4D8',
                            borderLeft: '2px solid transparent',
                            whiteSpace: 'nowrap',
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                          }}
                          onMouseenter={(e) => {
                            (e.currentTarget as HTMLElement).style.backgroundColor = 'rgba(255,255,255,0.05)'
                          }}
                          onMouseleave={(e) => {
                            (e.currentTarget as HTMLElement).style.backgroundColor = 'transparent'
                          }}
                        >
                          <span style={{ color: '#71717A', minWidth: '24px', textAlign: 'right' }}>
                            {m.line}:
                          </span>
                          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>
                            {m.lineText}
                          </span>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )
            })
          )}
        </div>
      </div>
    )
  },
})
