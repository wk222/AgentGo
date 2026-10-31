import { defineComponent, ref, reactive, computed, onMounted, onUnmounted, nextTick, watch } from 'vue'
import {
  Files,
  Search,
  GitBranch,
  ListTree,
  Sparkles,
  Terminal,
  Command,
  BookOpen,
  Save,
  Code2,
  PanelLeft,
} from 'lucide-vue-next'
import { useWorkspaceState } from './composables/useWorkspaceState'
import { useWorkspace } from '../../stores/useWorkspace'
import { useChat } from '../../stores/useChat'
import { useLayout } from '../../stores/useLayout'
import { stageState } from '../../stores/agentBridge'
import { useTabs } from './composables/useTabs'
import { FileTree } from './components/FileTree'
import { TabBar } from './components/TabBar'
import { Breadcrumbs } from './components/Breadcrumbs'
import { StatusBar } from './components/StatusBar'
import { EditorView } from './components/EditorView'
import { CommandPalette } from './components/CommandPalette'
import { SearchPanel } from './components/SearchPanel'
import { GitPanel } from './components/GitPanel'
import { OutlinePanel } from './components/OutlinePanel'
import { ReviewPanel } from './components/ReviewPanel'
import { DiffViewer } from './components/DiffViewer'
import { TerminalPanel } from './components/TerminalPanel'
import { MarkdownPreview } from './components/previews/MarkdownPreview'
import { ImagePreview } from './components/previews/ImagePreview'
import { wailsCall } from '../../shared/wails'
import type { WorkspaceSidebarView, CursorPositionInfo, CommandItem, WorkspaceFileItem, WorkspaceReviewChange } from './types'
import { notify, confirmDialog } from '../../shared/ui/notify'

const IMAGE_EXTENSIONS = new Set(['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'ico', 'bmp'])

export const Workspace = defineComponent({
  name: 'Workspace',
  props: {
    initialRoot: { type: String, default: '' },
    theme: { type: String, default: 'agentgo-dark' },
    sessionId: { type: String, default: '' },
  },
  emits: ['askAgent', 'explainCode', 'refactorCode'],
  setup(props, { emit }) {
    const wsState = useWorkspaceState(props.initialRoot)
    const tabState = useTabs()
    const wsStore = useWorkspace()
    const chat = useChat()
    const layout = useLayout()
    const resolvedTheme = computed(() => (layout.state.theme === 'dark' ? 'agentgo-dark' : 'agentgo-light'))

    const activeSidebarView = ref<WorkspaceSidebarView>('explorer')
    const sidebarWidth = ref(230)
    const isSidebarVisible = ref(true)
    const isTerminalOpen = ref(false)
    const isMarkdownPreview = ref(false)
    const isSplitMarkdown = ref(false)
    const rootPickerVisible = ref(false)
    const rootDraft = ref('')
    const reviewChanges = ref<WorkspaceReviewChange[]>([])

    // Diff view state
    const diffState = reactive<{
      active: boolean
      filePath: string
      originalContent: string
      modifiedContent: string
      originalTitle: string
      modifiedTitle: string
    }>({
      active: false,
      filePath: '',
      originalContent: '',
      modifiedContent: '',
      originalTitle: 'HEAD (原始)',
      modifiedTitle: '工作区 (修改后)',
    })

    // Cursor position info
    const cursorPos = reactive<CursorPositionInfo>({
      line: 1,
      column: 1,
      selectedChars: 0,
    })

    // Command Palette state
    const commandPalette = reactive<{
      visible: boolean
      mode: 'file' | 'command'
    }>({
      visible: false,
      mode: 'file',
    })

    // Editor View reference for line jumps
    const editorViewRef = ref<any>(null)

    // Check if active tab is image
    const isActiveTabImage = computed(() => {
      if (!tabState.activeTab.value) return false
      const ext = tabState.activeTab.value.name.split('.').pop()?.toLowerCase() || ''
      return IMAGE_EXTENSIONS.has(ext)
    })

    // Check if active tab is markdown
    const isActiveTabMarkdown = computed(() => {
      if (!tabState.activeTab.value) return false
      const ext = tabState.activeTab.value.name.split('.').pop()?.toLowerCase() || ''
      return ext === 'md' || ext === 'mdx' || ext === 'markdown'
    })

    // Registered IDE Commands for Command Palette
    const registeredCommands = computed<CommandItem[]>(() => [
      {
        id: 'file.save',
        title: '保存当前文件',
        category: 'File',
        shortcut: 'Ctrl+S',
        handler: () => tabState.saveActiveTab(),
      },
      {
        id: 'file.saveAll',
        title: '保存所有打开的文件',
        category: 'File',
        shortcut: 'Ctrl+K S',
        handler: () => tabState.saveAllTabs(),
      },
      {
        id: 'file.close',
        title: '关闭当前标签页',
        category: 'View',
        shortcut: 'Ctrl+W',
        handler: () => {
          if (tabState.activeTabPath.value) tabState.closeTab(tabState.activeTabPath.value)
        },
      },
      {
        id: 'file.closeAll',
        title: '关闭所有标签页',
        category: 'View',
        handler: () => tabState.closeAllTabs(),
      },
      {
        id: 'view.toggleTerminal',
        title: '切换终端控制台',
        category: 'Terminal',
        shortcut: 'Ctrl+`',
        handler: () => {
          isTerminalOpen.value = !isTerminalOpen.value
        },
      },
      {
        id: 'view.showExplorer',
        title: '切换至资源管理器 (Explorer)',
        category: 'View',
        shortcut: 'Ctrl+Shift+E',
        handler: () => {
          activeSidebarView.value = 'explorer'
          isSidebarVisible.value = true
        },
      },
      {
        id: 'view.showSearch',
        title: '切换至全局搜索 (Search)',
        category: 'View',
        shortcut: 'Ctrl+Shift+F',
        handler: () => {
          activeSidebarView.value = 'search'
          isSidebarVisible.value = true
        },
      },
      {
        id: 'view.showGit',
        title: '切换至源代码管理 (Git)',
        category: 'View',
        shortcut: 'Ctrl+Shift+G',
        handler: () => {
          activeSidebarView.value = 'git'
          isSidebarVisible.value = true
        },
      },
      {
        id: 'view.showOutline',
        title: '切换至代码大纲 (Outline)',
        category: 'View',
        handler: () => {
          activeSidebarView.value = 'outline'
          isSidebarVisible.value = true
        },
      },
      {
        id: 'editor.format',
        title: '格式化文档 (Format Document)',
        category: 'Editor',
        shortcut: 'Alt+Shift+F',
        handler: () => editorViewRef.value?.formatCode(),
      },
      {
        id: 'workspace.refresh',
        title: '刷新工作区文件',
        category: 'Workspace',
        handler: () => wsState.loadWorkspaceRoot(),
      },
    ])

    const openCommandPalette = (mode: 'file' | 'command') => {
      commandPalette.mode = mode
      commandPalette.visible = true
    }

    const openRootPicker = () => {
      rootDraft.value = wsState.workspaceRoot.value
      rootPickerVisible.value = true
    }

    const applyWorkspaceRoot = async () => {
      const nextRoot = rootDraft.value.trim()
      if (!nextRoot) return
      const ok = await wsStore.switchTo(nextRoot)
      if (ok) rootPickerVisible.value = false
      else if (wsStore.error.value) wsState.errorMsg.value = wsStore.error.value
    }

    const openFile = async (item: WorkspaceFileItem | { path: string; name: string }) => {
      diffState.active = false
      const tab = await tabState.openFile(item)
      if (tab) {
        if (isActiveTabMarkdown.value) {
          isMarkdownPreview.value = false
        }
      }
    }

    const openMatch = async (matchData: { filePath: string; line: number; column: number }) => {
      const fileName = matchData.filePath.split('/').pop() || matchData.filePath
      await openFile({ path: matchData.filePath, name: fileName })
      nextTick(() => {
        editorViewRef.value?.revealLine(matchData.line)
      })
    }

    const openDiff = async (item: { path: string; name: string }) => {
      try {
        const res = await wailsCall('WorkspaceFileDiff', item.path)
        if (res?.success === false || res?.error) {
          notify.error(`无法打开差异比对: ${res?.error || '未知错误'}`)
          return
        }

        const openTab = tabState.openTabs.value.find((tab) => tab.path === item.path)
        diffState.filePath = item.path
        diffState.originalContent = res?.original_content ?? ''
        diffState.modifiedContent = openTab?.model?.getValue() ?? openTab?.content ?? res?.modified_content ?? ''
        diffState.originalTitle = 'HEAD (原始)'
        diffState.modifiedTitle = '工作区 (修改后)'
        diffState.active = true
      } catch (e: any) {
        notify.error(`无法打开差异比对: ${e?.message || e}`)
      }
    }

    const openReviewDiff = (item: { path: string; originalContent: string; modifiedContent: string }) => {
      diffState.filePath = item.path
      diffState.originalContent = item.originalContent
      diffState.modifiedContent = item.modifiedContent
      diffState.originalTitle = 'AI 修改前'
      diffState.modifiedTitle = 'AI 修改后'
      diffState.active = true
    }

    const handleReviewRestore = async (path: string) => {
      await tabState.reloadPath(path)
      await wsState.loadWorkspaceRoot()
      if (diffState.filePath === path) diffState.active = false
    }

    const buildAgentPrompt = (action: 'ask' | 'explain' | 'refactor', code: string) => {
      const tab = tabState.activeTab.value
      const path = tab?.path || '当前文件'
      const task = action === 'explain'
        ? '请解释下面这段代码，指出关键逻辑、依赖和潜在风险。'
        : action === 'refactor'
          ? '请重构下面这段代码。先说明修改意图，再通过工作区工具实施修改，并给出可审查的差异。'
          : '请结合项目上下文分析下面这段代码并回答我的问题。'

      if (!code) {
        return `${task}\n\n目标文件：${path}\n请先读取该文件及必要的相邻代码，不要脱离项目上下文猜测。`
      }
      return `${task}\n\n目标文件：${path}\n\n\`\`\`${tab?.language || ''}\n${code}\n\`\`\``
    }

    watch(() => stageState.pendingEditorCommand, command => {
      if (!command) return
      openCommandPalette(command)
      stageState.pendingEditorCommand = ''
    }, {immediate:true})
    const handleGlobalKeydown = (e: KeyboardEvent) => {
      if (e.defaultPrevented || layout.state.agent === 'maximized' || layout.state.workbenchType !== 'code') return
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'l') {
        e.preventDefault()
        emit('askAgent', buildAgentPrompt('ask', editorViewRef.value?.getSelectedText?.() || ''))
      } else if ((e.ctrlKey || e.metaKey) && e.key === 'p' && !e.shiftKey) {
        e.preventDefault()
        openCommandPalette('file')
      } else if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key === 'P') {
        e.preventDefault()
        openCommandPalette('command')
      } else if ((e.ctrlKey || e.metaKey) && e.key === '`') {
        e.preventDefault()
        isTerminalOpen.value = !isTerminalOpen.value
      } else if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault()
        tabState.saveActiveTab()
      }
    }

    const diagnosticCount = ref(0)
    const refreshDiagnostics = async () => {
      try {
        const res = await wailsCall('WorkspaceDiagnostics')
        if (res?.success) {
          diagnosticCount.value = res.total ?? 0
        }
      } catch (e) {
        console.debug('[Workspace] refreshDiagnostics failed:', e)
      }
    }

    const refreshReview = async () => {
      if (!props.sessionId) {
        reviewChanges.value = []
        return
      }
      try {
        const res = await wailsCall('ListWorkspaceReview', props.sessionId)
        if (res?.success !== false && !res?.error) {
          reviewChanges.value = res?.changes || []
        }
      } catch {}
    }

    const openReviewChangeDiff = async (change: WorkspaceReviewChange) => {
      if (change.binary) {
        notify.info('二进制文件不能文本预览，但仍可接受或恢复。')
        return
      }
      try {
        const res = await wailsCall('WorkspaceReviewFile', props.sessionId, change.path)
        if (res?.success === false || res?.error) {
          notify.error(res?.error || '读取差异失败')
          return
        }
        openReviewDiff({
          path: change.path,
          originalContent: res?.original_content || '',
          modifiedContent: res?.modified_content || '',
        })
      } catch (e: any) {
        notify.error(e?.message || '读取差异失败')
      }
    }

    const acceptAllReview = async () => {
      await wailsCall('AcceptAllWorkspaceReview', props.sessionId)
      diffState.active = false
      await refreshReview()
    }

    const rejectAllReview = async () => {
      if (!reviewChanges.value.length) return
      const ok = await confirmDialog(`全部 ${reviewChanges.value.length} 个文件将恢复到本次 AI 修改前。`, { title: '恢复全部文件', okText: '全部恢复', danger: true })
      if (!ok) return
      const snapshot = [...reviewChanges.value]
      const res = await wailsCall('RejectAllWorkspaceReview', props.sessionId)
      if (res?.success === false || res?.error) {
        notify.error(res?.error || '全部恢复失败')
      } else {
        for (const c of snapshot) await handleReviewRestore(c.path)
        diffState.active = false
      }
      await refreshReview()
    }

    watch(() => [props.sessionId, chat.sending.value, chat.messages.value.length], () => {
      void refreshReview()
      if (!chat.sending.value) void refreshDiagnostics()
    })

    watch(() => [tabState.activeTab.value?.path || '',tabState.activeTab.value?.isDirty || false] as const, ([path,dirty]) => {
      stageState.activeFilePath=path
      stageState.activeFileDirty=dirty
    })
    watch(() => stageState.activeFilePath, (filePath) => {
      if (!filePath) return
      const name = filePath.replace(/\\/g, '/').split('/').pop() || filePath
      void openFile({ path: filePath, name })
    }, {immediate:true})

    // 未保存的标签会随工作区切换丢失：切换前（标题栏或本面板发起都一样）先确认。
    const releaseGuard = wsStore.registerSwitchGuard(async () => {
      const dirty = tabState.openTabs.value.filter(t => t.isDirty).length
      if (!dirty) return true
      return confirmDialog(`有 ${dirty} 个文件未保存，切换工作区会丢弃这些修改。`, { title: '切换工作区', okText: '丢弃并切换', danger: true })
    })
    // 工作区已在后端切换：旧标签的相对路径对新根无意义，全部关闭并重新加载。
    watch(wsStore.version, () => {
      tabState.closeAllTabs()
      diffState.active = false
      wsState.resetForNewRoot()
      void wsState.loadWorkspaceRoot()
      void refreshDiagnostics()
    })

    onMounted(() => {
      wsState.loadWorkspaceRoot()
      refreshDiagnostics()
      void refreshReview()
      window.addEventListener('keydown', handleGlobalKeydown)
    })

    onUnmounted(() => {
      releaseGuard()
      window.removeEventListener('keydown', handleGlobalKeydown)
    })

    return {
      wsState,
      tabState,
      activeSidebarView,
      sidebarWidth,
      isSidebarVisible,
      isTerminalOpen,
      isMarkdownPreview,
      isSplitMarkdown,
      rootPickerVisible,
      rootDraft,
      reviewChanges,
      diffState,
      cursorPos,
      commandPalette,
      editorViewRef,
      isActiveTabImage,
      isActiveTabMarkdown,
      registeredCommands,
      diagnosticCount,
      refreshDiagnostics,
      refreshReview,
      openReviewChangeDiff,
      acceptAllReview,
      rejectAllReview,
      openFile,
      openMatch,
      openDiff,
      openReviewDiff,
      handleReviewRestore,
      openCommandPalette,
      openRootPicker,
      applyWorkspaceRoot,
      onAskAgent: (code: string) => emit('askAgent', buildAgentPrompt('ask', code)),
      onExplainCode: (code: string) => emit('explainCode', buildAgentPrompt('explain', code)),
      onRefactorCode: (code: string) => emit('refactorCode', buildAgentPrompt('refactor', code)),
      onFixProblems: () => emit('askAgent', '@Problems 请检查当前工作区的语法与诊断错误并直接修复。'),
    }
  },
  render() {
    const navItems = [
      { id: 'explorer' as const, label: '文件', title: '资源管理器 (Ctrl+Shift+E)', Icon: Files },
      { id: 'search' as const, label: '搜索', title: '全局搜索 (Ctrl+Shift+F)', Icon: Search },
      { id: 'git' as const, label: 'Git', title: '源代码管理 (Ctrl+Shift+G)', Icon: GitBranch },
      { id: 'outline' as const, label: '大纲', title: '代码大纲 (Outline)', Icon: ListTree },
      { id: 'review' as const, label: '审查', title: 'AI 变更审查 (Review)', Icon: Sparkles },
    ]
    return (
      <div
        style={{
          display: 'flex',
          width: '100%',
          height: '100%',
          backgroundColor: 'var(--bg)',
          overflow: 'hidden',
          fontFamily: 'var(--font-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif)',
        }}
      >
        {/* Collapsible Sidebar Content Area (top segmented subnav replaces duplicate vertical rail) */}
        {this.isSidebarVisible && (
          <div class="ws-sidebar-pane" style={{ width: `${this.sidebarWidth}px` }}>
            <div class="ws-subnav">
              <div class="ws-subnav-tabs">
                {navItems.map(({ id, label, title, Icon }) => {
                  const active = this.activeSidebarView === id
                  return (
                    <button
                      key={id}
                      type="button"
                      class={['ws-subnav-btn', active && 'active']}
                      title={title}
                      onClick={() => { this.activeSidebarView = id }}
                    >
                      <Icon size={13} strokeWidth={1.9} />
                      <span>{label}</span>
                      {id === 'review' && this.reviewChanges.length > 0 && (
                        <span class="ws-subnav-badge">{this.reviewChanges.length}</span>
                      )}
                    </button>
                  )
                })}
              </div>
              <button
                type="button"
                class="ws-subnav-btn"
                title="收起文件侧栏"
                onClick={() => { this.isSidebarVisible = false }}
              >
                <PanelLeft size={13} />
              </button>
            </div>
            <div class="ws-subnav-body">
              {this.activeSidebarView === 'explorer' && (
                <FileTree
                  rootFiles={this.wsState.rootFiles.value}
                  workspaceRoot={this.wsState.workspaceRoot.value}
                  expandedPaths={this.wsState.expandedPaths.value}
                  childrenMap={this.wsState.childrenMap}
                  activePath={this.tabState.activeTabPath.value}
                  inlineCreating={this.wsState.inlineCreating}
                  inlineRenaming={this.wsState.inlineRenaming}
                  onSelectFile={this.openFile}
                  onToggleDir={this.wsState.toggleDirectory}
                  onStartCreate={this.wsState.startInlineCreate}
                  onSubmitCreate={this.wsState.submitInlineCreate}
                  onCancelCreate={this.wsState.cancelInlineCreate}
                  onStartRename={this.wsState.startInlineRename}
                  onSubmitRename={this.wsState.submitInlineRename}
                  onCancelRename={this.wsState.cancelInlineRename}
                  onConfirmDelete={this.wsState.confirmDelete}
                  onRefresh={this.wsState.loadWorkspaceRoot}
                  onChangeRoot={this.openRootPicker}
                />
              )}

              {this.activeSidebarView === 'search' && (
                <SearchPanel
                  activePath={this.tabState.activeTabPath.value}
                  onOpenMatch={this.openMatch}
                  onReplaceFileContent={(path, content) => {
                    const t = this.tabState.openTabs.value.find((x) => x.path === path)
                    if (t) {
                      t.content = content
                      t.originalContent = content
                      t.isDirty = false
                      if (t.model) t.model.setValue(content)
                    }
                  }}
                />
              )}

              {this.activeSidebarView === 'git' && (
                <GitPanel
                  activePath={this.tabState.activeTabPath.value}
                  onOpenDiff={this.openDiff}
                  onSelectFile={this.openFile}
                />
              )}

              {this.activeSidebarView === 'outline' && (
                <OutlinePanel
                  activeTab={this.tabState.activeTab.value}
                  onJumpToLine={(line) => this.editorViewRef?.revealLine(line)}
                />
              )}

              {this.activeSidebarView === 'review' && (
                <ReviewPanel
                  sessionId={this.sessionId}
                  onOpenDiff={this.openReviewDiff}
                  onFileRestored={(p: string) => { void this.handleReviewRestore(p); void this.refreshReview() }}
                  onFileAccepted={(path: string) => { if (this.diffState.filePath === path) this.diffState.active = false; void this.refreshReview() }}
                  onReviewCompleted={() => { this.diffState.active = false; void this.refreshReview() }}
                />
              )}
            </div>
          </div>
        )}

        {/* Main Work Area */}
        <div
          class="ws-main-workarea"
          style={{
            flex: 1,
            display: 'flex',
            flexDirection: 'column',
            height: '100%',
            overflow: 'hidden',
            backgroundColor: 'var(--editor-bg)',
          }}
        >
          {/* Top Tab Bar & Toolbar */}
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              backgroundColor: 'var(--tab-bg)',
              borderBottom: '1px solid var(--border)',
              overflow: 'hidden',
            }}
          >
            {/* Sidebar Toggle (when collapsed) + Tabs List */}
            {!this.isSidebarVisible && (
              <button
                type="button"
                class="ws-toolbar-btn"
                style={{ marginLeft: '6px' }}
                title="展开文件侧栏"
                onClick={() => { this.isSidebarVisible = true }}
              >
                <PanelLeft size={13} />
              </button>
            )}
            <div style={{ flex: 1, overflow: 'hidden' }}>
              <TabBar
                tabs={this.tabState.openTabs.value}
                activeTabPath={this.tabState.activeTabPath.value}
                onSelectTab={(path) => {
                  this.diffState.active = false
                  this.tabState.activeTabPath.value = path
                }}
                onCloseTab={this.tabState.closeTab}
                onCloseOtherTabs={this.tabState.closeOtherTabs}
                onCloseAllTabs={this.tabState.closeAllTabs}
              />
            </div>

            {/* Quick Actions in Tab Bar Right Corner */}
            <div style={{ display: 'flex', alignItems: 'center', gap: '4px', paddingRight: '8px', paddingLeft: '4px' }}>
              {this.isActiveTabMarkdown && (
                <button
                  type="button"
                  class={['ws-toolbar-btn', this.isMarkdownPreview && 'active']}
                  onClick={() => { this.isMarkdownPreview = !this.isMarkdownPreview }}
                  title="Markdown 实时预览"
                >
                  <BookOpen size={12} />
                  <span>预览</span>
                </button>
              )}

              <button
                type="button"
                class={['ws-toolbar-btn', this.isTerminalOpen && 'active']}
                onClick={() => { this.isTerminalOpen = !this.isTerminalOpen }}
                title="切换终端 (Ctrl+`)"
              >
                <Terminal size={12} />
                <span>终端</span>
              </button>

              <button
                type="button"
                class="ws-toolbar-btn"
                onClick={() => this.openCommandPalette('command')}
                title="命令面板 (Ctrl+Shift+P)"
              >
                <Command size={12} />
              </button>

              {this.tabState.activeTab.value?.isDirty && (
                <button
                  type="button"
                  class="ws-toolbar-btn primary"
                  onClick={() => this.tabState.saveActiveTab()}
                  title="保存文件 (Ctrl+S)"
                >
                  <Save size={12} />
                  <span>保存</span>
                </button>
              )}
            </div>
          </div>

          {/* Inline AI Review Bar: surfaces pending AI file modifications directly above the editor */}
          {this.reviewChanges.length > 0 && (
            <div class="ws-inline-review-bar">
              <div class="ws-inline-review-left">
                <Sparkles size={13} style={{ color: 'var(--accent)', flexShrink: 0 }} />
                <strong style={{ color: 'var(--accent-text)' }}>AI 已修改 {this.reviewChanges.length} 个文件：</strong>
                {this.reviewChanges.slice(0, 6).map((c) => (
                  <button
                    key={c.path}
                    type="button"
                    class="ws-inline-review-pill"
                    onClick={() => void this.openReviewChangeDiff(c)}
                    title={`点击查看 Diff (${c.status})`}
                  >
                    <span>{c.path.split('/').pop() || c.path}</span>
                  </button>
                ))}
                {this.reviewChanges.length > 6 && (
                  <span style={{ color: 'var(--text-dim)' }}>+{this.reviewChanges.length - 6}</span>
                )}
              </div>
              <div class="ws-inline-review-actions">
                <button
                  type="button"
                  class="ws-toolbar-btn"
                  onClick={() => { this.activeSidebarView = 'review'; this.isSidebarVisible = true }}
                >
                  逐个审查
                </button>
                <button type="button" class="ws-toolbar-btn active" onClick={this.acceptAllReview}>
                  全部接受
                </button>
                <button type="button" class="ws-toolbar-btn danger" onClick={this.rejectAllReview}>
                  全部恢复
                </button>
              </div>
            </div>
          )}

          {/* Breadcrumbs Path Navigation */}
          {this.tabState.activeTab.value && !this.diffState.active && (
            <Breadcrumbs
              filePath={this.tabState.activeTab.value.path}
              workspaceRoot={this.wsState.workspaceRoot.value}
            />
          )}

          {/* Center Editor / Viewer Viewport */}
          <div style={{ flex: 1, position: 'relative', overflow: 'hidden' }}>
            {this.diffState.active ? (
              <DiffViewer
                filePath={this.diffState.filePath}
                originalContent={this.diffState.originalContent}
                modifiedContent={this.diffState.modifiedContent}
                originalTitle={this.diffState.originalTitle}
                modifiedTitle={this.diffState.modifiedTitle}
                onClose={() => { this.diffState.active = false }}
              />
            ) : this.isActiveTabImage && this.tabState.activeTab.value ? (
              <ImagePreview
                filePath={this.tabState.activeTab.value.path}
                fileName={this.tabState.activeTab.value.name}
              />
            ) : this.isMarkdownPreview && this.tabState.activeTab.value ? (
              <MarkdownPreview
                content={this.tabState.activeTab.value.content}
                fileName={this.tabState.activeTab.value.name}
              />
            ) : this.tabState.activeTab.value ? (
              <EditorView
                ref="editorViewRef"
                tab={this.tabState.activeTab.value}
                theme={this.resolvedTheme}
                onCursorChange={(pos: CursorPositionInfo) => {
                  this.cursorPos.line = pos.line
                  this.cursorPos.column = pos.column
                  this.cursorPos.selectedChars = pos.selectedChars
                }}
                onSave={() => this.tabState.saveActiveTab()}
                onOpenCommandPalette={this.openCommandPalette}
                onAskAgent={this.onAskAgent}
                onExplainCode={this.onExplainCode}
                onRefactorCode={this.onRefactorCode}
              />
            ) : (
              <div class="workspace-empty-state">
                <div class="workspace-empty-icon">
                  <Code2 size={26} strokeWidth={1.75} />
                </div>
                <div style={{ fontSize: '15px', fontWeight: 600, color: 'var(--text-strong)', marginBottom: '6px' }}>
                  AgentGo Code Workspace
                </div>
                <div style={{ fontSize: '12px', color: 'var(--text-dim)', marginBottom: '22px' }}>
                  专业本地开发工作区 · 原生 Monaco IDE 引擎
                </div>
                <div class="workspace-empty-shortcuts">
                  {[
                    ['快速打开文件', 'Ctrl + P'],
                    ['运行 IDE 命令', 'Ctrl + Shift + P'],
                    ['发送代码给 Agent', 'Ctrl + L'],
                    ['AI 内联代码编辑', 'Ctrl + K'],
                    ['切换终端控制台', 'Ctrl + `'],
                  ].map(([label, key]) => (
                    <div key={label} class="workspace-empty-row">
                      <span>{label}</span>
                      <kbd>{key}</kbd>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>

          {/* Bottom Resizable Terminal Drawer */}
          {this.isTerminalOpen && (
            <TerminalPanel
              height={220}
              workspaceRoot={this.wsState.workspaceRoot.value}
              onClose={() => { this.isTerminalOpen = false }}
            />
          )}

          {/* Bottom Status Bar */}
          <StatusBar
            activeTab={this.tabState.activeTab.value}
            cursorPos={this.cursorPos}
            gitBranch={this.wsState.gitBranch.value}
            isTerminalOpen={this.isTerminalOpen}
            diagnosticCount={this.diagnosticCount}
            onToggleTerminal={() => { this.isTerminalOpen = !this.isTerminalOpen }}
            onOpenCommandPalette={() => this.openCommandPalette('command')}
            onFormatDocument={() => this.editorViewRef?.formatCode()}
            onCheckProblems={this.refreshDiagnostics}
            onFixProblems={this.onFixProblems}
          />
        </div>

        {/* Global Command Palette Popup (Ctrl+P / Ctrl+Shift+P) */}
        <CommandPalette
          visible={this.commandPalette.visible}
          mode={this.commandPalette.mode}
          allFiles={this.wsState.rootFiles.value}
          commands={this.registeredCommands}
          onClose={() => { this.commandPalette.visible = false }}
          onSelectFile={this.openFile}
          onExecuteCommand={(cmd) => cmd.handler()}
        />

        {this.wsState.deleteModal.visible && this.wsState.deleteModal.item && (
          <div class="ws-modal-backdrop" onClick={this.wsState.cancelDelete}>
            <div class="ws-modal-card" onClick={(e) => e.stopPropagation()}>
              <div style={{ fontSize: '14px', fontWeight: 700, marginBottom: '8px', color: 'var(--text-strong)' }}>确认删除</div>
              <div style={{ fontSize: '12px', lineHeight: 1.6, color: 'var(--text-dim)', wordBreak: 'break-all' }}>
                将永久删除“{this.wsState.deleteModal.item.path}”。此操作无法撤销。
              </div>
              <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '8px', marginTop: '16px' }}>
                <button class="btn-secondary" onClick={this.wsState.cancelDelete}>取消</button>
                <button class="btn-danger" onClick={this.wsState.executeDelete}>删除</button>
              </div>
            </div>
          </div>
        )}

        {this.rootPickerVisible && (
          <div class="ws-modal-backdrop" onClick={() => { this.rootPickerVisible = false }}>
            <div class="ws-modal-card wide" onClick={(e) => e.stopPropagation()}>
              <div style={{ fontSize: '14px', fontWeight: 700, marginBottom: '6px', color: 'var(--text-strong)' }}>打开工作区</div>
              <div style={{ fontSize: '12px', color: 'var(--text-dim)', marginBottom: '10px' }}>输入本机项目文件夹的完整路径。</div>
              <input
                class="ws-modal-input"
                value={this.rootDraft}
                autofocus
                onInput={(e) => { this.rootDraft = (e.target as HTMLInputElement).value }}
                onKeydown={(e) => {
                  if (e.key === 'Enter') void this.applyWorkspaceRoot()
                  if (e.key === 'Escape') this.rootPickerVisible = false
                }}
              />
              {this.wsState.errorMsg.value && <div style={{ marginTop: '8px', color: 'var(--error)', fontSize: '11px' }}>{this.wsState.errorMsg.value}</div>}
              <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '8px', marginTop: '16px' }}>
                <button class="btn-secondary" onClick={() => { this.rootPickerVisible = false }}>取消</button>
                <button class="btn-primary" onClick={this.applyWorkspaceRoot}>打开</button>
              </div>
            </div>
          </div>
        )}
      </div>
    )
  },
})

export default Workspace
