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
} from 'lucide-vue-next'
import { useWorkspaceState } from './composables/useWorkspaceState'
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
import { wailsCall } from '../../wails'
import type { WorkspaceSidebarView, CursorPositionInfo, CommandItem, WorkspaceFileItem } from './types'

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

    const activeSidebarView = ref<WorkspaceSidebarView>('explorer')
    const sidebarWidth = ref(220)
    const isSidebarVisible = ref(true)
    const isTerminalOpen = ref(false)
    const isMarkdownPreview = ref(false)
    const isSplitMarkdown = ref(false)
    const rootPickerVisible = ref(false)
    const rootDraft = ref('')

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
      await wsState.loadWorkspaceRoot(nextRoot)
      if (!wsState.errorMsg.value) rootPickerVisible.value = false
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
        const res = await wailsCall<{
          success?: boolean
          error?: string
          original_content?: string
          modified_content?: string
        }>('WorkspaceFileDiff', item.path)
        if (res?.success === false || res?.error) {
          alert(`无法打开差异比对: ${res?.error || '未知错误'}`)
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
        alert(`无法打开差异比对: ${e?.message || e}`)
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

    const handleGlobalKeydown = (e: KeyboardEvent) => {
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
        const res = await wailsCall<any>('WorkspaceDiagnostics')
        if (res?.success) {
          diagnosticCount.value = res.total ?? 0
        }
      } catch (e) {
        console.debug('[Workspace] refreshDiagnostics failed:', e)
      }
    }

    onMounted(() => {
      wsState.loadWorkspaceRoot()
      refreshDiagnostics()
      window.addEventListener('keydown', handleGlobalKeydown)
    })

    onUnmounted(() => {
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
      diffState,
      cursorPos,
      commandPalette,
      editorViewRef,
      isActiveTabImage,
      isActiveTabMarkdown,
      registeredCommands,
      diagnosticCount,
      refreshDiagnostics,
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
    }
  },
  render() {
    return (
      <div
        style={{
          display: 'flex',
          width: '100%',
          height: '100%',
          backgroundColor: '#18181B',
          overflow: 'hidden',
          fontFamily: 'var(--font-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif)',
        }}
      >
        {/* Far-Left Activity Bar (VS Code / Cursor style 46px wide) */}
        <div
          class="ws-activitybar"
          style={{
            width: '46px',
            backgroundColor: 'var(--activitybar-bg)',
            borderRight: '1px solid var(--border)',
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            paddingTop: '6px',
            gap: '4px',
            userSelect: 'none',
            flexShrink: 0,
            zIndex: 10,
          }}
        >
          {/* Explorer Icon Button */}
          <button
            onClick={() => {
              if (this.activeSidebarView === 'explorer' && this.isSidebarVisible) {
                this.isSidebarVisible = false
              } else {
                this.activeSidebarView = 'explorer'
                this.isSidebarVisible = true
              }
            }}
            title="资源管理器 (Explorer)"
            style={{
              width: '36px',
              height: '36px',
              borderRadius: '6px',
              border: 'none',
              backgroundColor: this.isSidebarVisible && this.activeSidebarView === 'explorer' ? 'var(--accent-bg)' : 'transparent',
              color: this.isSidebarVisible && this.activeSidebarView === 'explorer' ? 'var(--accent)' : 'var(--activitybar-fg)',
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              position: 'relative',
              transition: 'all 0.12s ease',
            }}
          >
            {this.isSidebarVisible && this.activeSidebarView === 'explorer' && (
              <span style={{ position: 'absolute', left: 0, top: '8px', bottom: '8px', width: '2px', backgroundColor: 'var(--accent)', borderRadius: '0 2px 2px 0' }} />
            )}
            <Files size={18} strokeWidth={1.8} />
          </button>

          {/* Search Icon Button */}
          <button
            onClick={() => {
              if (this.activeSidebarView === 'search' && this.isSidebarVisible) {
                this.isSidebarVisible = false
              } else {
                this.activeSidebarView = 'search'
                this.isSidebarVisible = true
              }
            }}
            title="全局搜索 (Search)"
            style={{
              width: '36px',
              height: '36px',
              borderRadius: '6px',
              border: 'none',
              backgroundColor: this.isSidebarVisible && this.activeSidebarView === 'search' ? 'var(--accent-bg)' : 'transparent',
              color: this.isSidebarVisible && this.activeSidebarView === 'search' ? 'var(--accent)' : 'var(--activitybar-fg)',
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              position: 'relative',
              transition: 'all 0.12s ease',
            }}
          >
            {this.isSidebarVisible && this.activeSidebarView === 'search' && (
              <span style={{ position: 'absolute', left: 0, top: '8px', bottom: '8px', width: '2px', backgroundColor: 'var(--accent)', borderRadius: '0 2px 2px 0' }} />
            )}
            <Search size={18} strokeWidth={1.8} />
          </button>

          {/* Git Source Control Icon Button */}
          <button
            onClick={() => {
              if (this.activeSidebarView === 'git' && this.isSidebarVisible) {
                this.isSidebarVisible = false
              } else {
                this.activeSidebarView = 'git'
                this.isSidebarVisible = true
              }
            }}
            title="源代码管理 (Git)"
            style={{
              width: '36px',
              height: '36px',
              borderRadius: '6px',
              border: 'none',
              backgroundColor: this.isSidebarVisible && this.activeSidebarView === 'git' ? 'var(--accent-bg)' : 'transparent',
              color: this.isSidebarVisible && this.activeSidebarView === 'git' ? 'var(--accent)' : 'var(--activitybar-fg)',
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              position: 'relative',
              transition: 'all 0.12s ease',
            }}
          >
            {this.isSidebarVisible && this.activeSidebarView === 'git' && (
              <span style={{ position: 'absolute', left: 0, top: '8px', bottom: '8px', width: '2px', backgroundColor: 'var(--accent)', borderRadius: '0 2px 2px 0' }} />
            )}
            <GitBranch size={18} strokeWidth={1.8} />
          </button>

          {/* Outline Icon Button */}
          <button
            onClick={() => {
              if (this.activeSidebarView === 'outline' && this.isSidebarVisible) {
                this.isSidebarVisible = false
              } else {
                this.activeSidebarView = 'outline'
                this.isSidebarVisible = true
              }
            }}
            title="代码大纲 (Outline)"
            style={{
              width: '36px',
              height: '36px',
              borderRadius: '6px',
              border: 'none',
              backgroundColor: this.isSidebarVisible && this.activeSidebarView === 'outline' ? 'var(--accent-bg)' : 'transparent',
              color: this.isSidebarVisible && this.activeSidebarView === 'outline' ? 'var(--accent)' : 'var(--activitybar-fg)',
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              position: 'relative',
              transition: 'all 0.12s ease',
            }}
          >
            {this.isSidebarVisible && this.activeSidebarView === 'outline' && (
              <span style={{ position: 'absolute', left: 0, top: '8px', bottom: '8px', width: '2px', backgroundColor: 'var(--accent)', borderRadius: '0 2px 2px 0' }} />
            )}
            <ListTree size={18} strokeWidth={1.8} />
          </button>

          {/* Agent change review */}
          <button
            onClick={() => {
              if (this.activeSidebarView === 'review' && this.isSidebarVisible) {
                this.isSidebarVisible = false
              } else {
                this.activeSidebarView = 'review'
                this.isSidebarVisible = true
              }
            }}
            title="AI 变更审查"
            style={{
              width: '36px',
              height: '36px',
              borderRadius: '6px',
              border: 'none',
              backgroundColor: this.isSidebarVisible && this.activeSidebarView === 'review' ? 'var(--accent-bg-strong)' : 'transparent',
              color: this.isSidebarVisible && this.activeSidebarView === 'review' ? 'var(--accent)' : 'var(--activitybar-fg)',
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              position: 'relative',
              transition: 'all 0.12s ease',
            }}
          >
            {this.isSidebarVisible && this.activeSidebarView === 'review' && (
              <span style={{ position: 'absolute', left: 0, top: '8px', bottom: '8px', width: '2px', backgroundColor: 'var(--accent)', borderRadius: '0 2px 2px 0' }} />
            )}
            <Sparkles size={18} strokeWidth={1.8} />
          </button>

          {/* Bottom Activity Bar Icons */}
          <div style={{ marginTop: 'auto', marginBottom: '8px', display: 'flex', flexDirection: 'column', gap: '6px' }}>
            <button
              onClick={() => { this.isTerminalOpen = !this.isTerminalOpen }}
              title="切换集成终端 (Ctrl+`)"
              style={{
                width: '36px',
                height: '36px',
                borderRadius: '6px',
                border: 'none',
                backgroundColor: this.isTerminalOpen ? 'var(--accent-bg)' : 'transparent',
                color: this.isTerminalOpen ? 'var(--accent)' : 'var(--activitybar-fg)',
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                transition: 'all 0.12s ease',
              }}
            >
              <Terminal size={17} strokeWidth={1.8} />
            </button>
            <button
              onClick={() => this.openCommandPalette('command')}
              title="命令面板 (Ctrl+Shift+P)"
              style={{
                width: '36px',
                height: '36px',
                borderRadius: '6px',
                border: 'none',
                backgroundColor: 'transparent',
                color: 'var(--activitybar-fg)',
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                transition: 'all 0.12s ease',
              }}
            >
              <Command size={17} strokeWidth={1.8} />
            </button>
          </div>
        </div>

        {/* Collapsible Sidebar Content Area */}
        {this.isSidebarVisible && (
          <div
            class="ws-sidebar-pane"
            style={{
              width: `${this.sidebarWidth}px`,
              minWidth: '200px',
              maxWidth: '480px',
              height: '100%',
              backgroundColor: 'var(--sidebar)',
              borderRight: '1px solid var(--border)',
              overflow: 'hidden',
              flexShrink: 0,
            }}
          >
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
                onFileRestored={this.handleReviewRestore}
                onFileAccepted={(path: string) => { if (this.diffState.filePath === path) this.diffState.active = false }}
                onReviewCompleted={() => { this.diffState.active = false }}
              />
            )}
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
            {/* Tabs List */}
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
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '4px',
                paddingRight: '8px',
                paddingLeft: '4px',
              }}
            >
              {/* Markdown Preview Switcher */}
              {this.isActiveTabMarkdown && (
                <button
                  onClick={() => { this.isMarkdownPreview = !this.isMarkdownPreview }}
                  title="Markdown 实时预览"
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '4px',
                    padding: '3px 8px',
                    backgroundColor: this.isMarkdownPreview ? 'var(--accent-bg)' : 'transparent',
                    color: this.isMarkdownPreview ? 'var(--accent)' : 'var(--text-dim)',
                    border: '1px solid',
                    borderColor: this.isMarkdownPreview ? 'var(--accent-bg-strong)' : 'transparent',
                    borderRadius: '4px',
                    cursor: 'pointer',
                    fontSize: '11px',
                    fontWeight: 500,
                  }}
                >
                  <BookOpen size={12} />
                  <span>预览</span>
                </button>
              )}

              {/* Terminal Drawer Toggle Button */}
              <button
                onClick={() => { this.isTerminalOpen = !this.isTerminalOpen }}
                title="切换终端 (Ctrl+`)"
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '4px',
                  padding: '3px 8px',
                  backgroundColor: this.isTerminalOpen ? 'var(--accent-bg)' : 'transparent',
                  color: this.isTerminalOpen ? 'var(--accent)' : 'var(--text-dim)',
                  border: '1px solid',
                  borderColor: this.isTerminalOpen ? 'var(--accent-bg-strong)' : 'transparent',
                  borderRadius: '4px',
                  cursor: 'pointer',
                  fontSize: '11px',
                  fontWeight: 500,
                }}
              >
                <Terminal size={12} />
                <span>终端</span>
              </button>

              {/* Save Button */}
              {this.tabState.activeTab.value?.isDirty && (
                <button
                  onClick={() => this.tabState.saveActiveTab()}
                  title="保存文件 (Ctrl+S)"
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '4px',
                    padding: '3px 8px',
                    backgroundColor: 'var(--accent)',
                    color: '#ffffff',
                    border: 'none',
                    borderRadius: '4px',
                    cursor: 'pointer',
                    fontSize: '11px',
                    fontWeight: 600,
                  }}
                >
                  <Save size={12} />
                  <span>保存</span>
                </button>
              )}
            </div>
          </div>

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
                theme={this.theme}
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
              /* Empty Workspace Center Placeholder (Cursor Style) */
              <div
                class="workspace-empty-state"
                style={{
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: 'center',
                  justifyContent: 'center',
                  height: '100%',
                  color: 'var(--text-dim)',
                  userSelect: 'none',
                  padding: '24px',
                }}
              >
                <div style={{
                  width: '52px',
                  height: '52px',
                  borderRadius: '12px',
                  background: 'var(--surface-hover)',
                  border: '1px solid var(--border-subtle)',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  marginBottom: '14px',
                  color: 'var(--accent)',
                }}>
                  <Code2 size={26} strokeWidth={1.75} />
                </div>
                <div style={{ fontSize: '15px', fontWeight: 600, color: 'var(--text-strong)', marginBottom: '6px' }}>
                  AgentGo Code Workspace
                </div>
                <div style={{ fontSize: '12px', color: 'var(--text-dim)', marginBottom: '22px' }}>
                  专业本地开发工作区 · 原生 Monaco IDE 引擎
                </div>
                <div style={{
                  display: 'flex',
                  flexDirection: 'column',
                  gap: '8px',
                  minWidth: '290px',
                  padding: '12px 16px',
                  borderRadius: '8px',
                  background: 'var(--surface-hover)',
                  border: '1px solid var(--border)',
                }}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: '12px' }}>
                    <span style={{ color: 'var(--text)' }}>快速打开文件</span>
                    <kbd style={{ padding: '2px 6px', background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: '4px', color: 'var(--text-strong)', fontFamily: 'var(--font-mono)', fontSize: '11px' }}>Ctrl + P</kbd>
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: '12px' }}>
                    <span style={{ color: 'var(--text)' }}>运行 IDE 命令</span>
                    <kbd style={{ padding: '2px 6px', background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: '4px', color: 'var(--text-strong)', fontFamily: 'var(--font-mono)', fontSize: '11px' }}>Ctrl + Shift + P</kbd>
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: '12px' }}>
                    <span style={{ color: 'var(--text)' }}>发送代码给 Agent</span>
                    <kbd style={{ padding: '2px 6px', background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: '4px', color: 'var(--text-strong)', fontFamily: 'var(--font-mono)', fontSize: '11px' }}>Ctrl + L</kbd>
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: '12px' }}>
                    <span style={{ color: 'var(--text)' }}>AI 内联代码编辑</span>
                    <kbd style={{ padding: '2px 6px', background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: '4px', color: 'var(--text-strong)', fontFamily: 'var(--font-mono)', fontSize: '11px' }}>Ctrl + K</kbd>
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: '12px' }}>
                    <span style={{ color: 'var(--text)' }}>切换终端控制台</span>
                    <kbd style={{ padding: '2px 6px', background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: '4px', color: 'var(--text-strong)', fontFamily: 'var(--font-mono)', fontSize: '11px' }}>Ctrl + `</kbd>
                  </div>
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
          <div
            style={{
              position: 'fixed',
              inset: 0,
              zIndex: 1100,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              backgroundColor: 'rgba(0, 0, 0, 0.62)',
            }}
            onClick={this.wsState.cancelDelete}
          >
            <div
              style={{
                width: '380px',
                maxWidth: 'calc(100vw - 32px)',
                padding: '18px',
                borderRadius: '10px',
                border: '1px solid rgba(255,255,255,0.12)',
                backgroundColor: '#1E1E22',
                boxShadow: '0 18px 48px rgba(0,0,0,0.5)',
                color: '#E4E4E7',
              }}
              onClick={(e) => e.stopPropagation()}
            >
              <div style={{ fontSize: '15px', fontWeight: 700, marginBottom: '8px' }}>确认删除</div>
              <div style={{ fontSize: '12px', lineHeight: 1.6, color: '#A1A1AA', wordBreak: 'break-all' }}>
                将永久删除“{this.wsState.deleteModal.item.path}”。此操作无法撤销。
              </div>
              <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '8px', marginTop: '18px' }}>
                <button
                  onClick={this.wsState.cancelDelete}
                  style={{ padding: '6px 12px', borderRadius: '5px', border: '1px solid rgba(255,255,255,0.12)', background: 'transparent', color: '#D4D4D8', cursor: 'pointer' }}
                >
                  取消
                </button>
                <button
                  onClick={this.wsState.executeDelete}
                  style={{ padding: '6px 12px', borderRadius: '5px', border: 'none', background: '#DC2626', color: '#FFFFFF', cursor: 'pointer', fontWeight: 700 }}
                >
                  删除
                </button>
              </div>
            </div>
          </div>
        )}

        {this.rootPickerVisible && (
          <div
            style={{ position: 'fixed', inset: 0, zIndex: 1100, display: 'flex', alignItems: 'center', justifyContent: 'center', backgroundColor: 'rgba(0,0,0,0.62)' }}
            onClick={() => { this.rootPickerVisible = false }}
          >
            <div
              style={{ width: '520px', maxWidth: 'calc(100vw - 32px)', padding: '18px', borderRadius: '10px', border: '1px solid rgba(255,255,255,0.12)', backgroundColor: '#1E1E22', boxShadow: '0 18px 48px rgba(0,0,0,0.5)', color: '#E4E4E7' }}
              onClick={(e) => e.stopPropagation()}
            >
              <div style={{ fontSize: '15px', fontWeight: 700, marginBottom: '8px' }}>打开工作区</div>
              <div style={{ fontSize: '12px', color: '#A1A1AA', marginBottom: '10px' }}>输入本机项目文件夹的完整路径。</div>
              <input
                value={this.rootDraft}
                autofocus
                onInput={(e) => { this.rootDraft = (e.target as HTMLInputElement).value }}
                onKeydown={(e) => {
                  if (e.key === 'Enter') void this.applyWorkspaceRoot()
                  if (e.key === 'Escape') this.rootPickerVisible = false
                }}
                style={{ width: '100%', boxSizing: 'border-box', padding: '8px 10px', borderRadius: '5px', border: '1px solid rgba(255,255,255,0.14)', outline: 'none', backgroundColor: '#121214', color: '#FFFFFF', fontSize: '12px', fontFamily: 'monospace' }}
              />
              {this.wsState.errorMsg.value && <div style={{ marginTop: '8px', color: '#F87171', fontSize: '11px' }}>{this.wsState.errorMsg.value}</div>}
              <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '8px', marginTop: '16px' }}>
                <button onClick={() => { this.rootPickerVisible = false }} style={{ padding: '6px 12px', borderRadius: '5px', border: '1px solid rgba(255,255,255,0.12)', background: 'transparent', color: '#D4D4D8', cursor: 'pointer' }}>取消</button>
                <button onClick={this.applyWorkspaceRoot} style={{ padding: '6px 12px', borderRadius: '5px', border: 'none', background: '#38BDF8', color: '#09090B', cursor: 'pointer', fontWeight: 700 }}>打开</button>
              </div>
            </div>
          </div>
        )}
      </div>
    )
  },
})

export default Workspace
