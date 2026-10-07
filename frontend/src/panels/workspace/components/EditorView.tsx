import { defineComponent, ref, onMounted, onUnmounted, watch, nextTick, type PropType } from 'vue'
import * as monaco from 'monaco-editor'
import { setupMonaco, getMonacoLanguage } from '../../../utils/monaco'
import { wailsCall } from '../../../wails'
import type { OpenTab, CursorPositionInfo } from '../types'

export const EditorView = defineComponent({
  name: 'EditorView',
  props: {
    tab: { type: Object as PropType<OpenTab | null>, default: null },
    fontSize: { type: Number, default: 13 },
    tabSize: { type: Number, default: 2 },
    wordWrap: { type: Boolean, default: true },
    theme: { type: String, default: 'agentgo-dark' },
  },
  emits: ['cursorChange', 'save', 'openCommandPalette', 'askAgent', 'explainCode', 'refactorCode'],
  setup(props, { emit }) {
    const editorContainerRef = ref<HTMLDivElement | null>(null)
    const inlineInputRef = ref<HTMLInputElement | null>(null)
    let editorInstance: monaco.editor.IStandaloneCodeEditor | null = null
    let resizeObserver: ResizeObserver | null = null
    let isUpdatingModel = false
    let inlineDecorations: string[] = []

    const inlineEdit = ref<{
      visible: boolean
      x: number
      y: number
      instruction: string
      loading: boolean
      error: string
      replacement: string | null
      summary: string
      range: monaco.Range | null
      originalText: string
    }>({
      visible: false,
      x: 16,
      y: 48,
      instruction: '',
      loading: false,
      error: '',
      replacement: null,
      summary: '',
      range: null,
      originalText: '',
    })

    // Quick Action Bar for selected code
    const selectionBar = ref<{
      visible: boolean
      x: number
      y: number
      selectedText: string
    }>({
      visible: false,
      x: 0,
      y: 0,
      selectedText: '',
    })

    const initEditor = () => {
      if (!editorContainerRef.value) return
      setupMonaco()

      editorInstance = monaco.editor.create(editorContainerRef.value, {
        value: props.tab?.content || '',
        language: props.tab ? getMonacoLanguage(props.tab.name) : 'plaintext',
        theme: props.theme,
        fontSize: props.fontSize,
        tabSize: props.tabSize,
        fontFamily: "'Fira Code', 'Cascadia Code', 'JetBrains Mono', 'Consolas', monospace",
        fontLigatures: true,
        wordWrap: props.wordWrap ? 'on' : 'off',
        automaticLayout: true,
        minimap: { enabled: true, maxColumn: 80, renderCharacters: false },
        scrollBeyondLastLine: false,
        renderWhitespace: 'selection',
        lineNumbers: 'on',
        renderLineHighlight: 'all',
        cursorBlinking: 'smooth',
        cursorSmoothCaretAnimation: 'on',
        smoothScrolling: true,
        contextmenu: true,
        folding: true,
        foldingHighlight: true,
        bracketPairColorization: { enabled: true },
        padding: { top: 10, bottom: 10 },
      })

      // Keybindings
      editorInstance.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
        emit('save')
      })

      editorInstance.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyP, () => {
        emit('openCommandPalette', 'file')
      })

      // Cursor-style shortcut: send the current selection (or current file)
      // to the Agent panel. Register it in Monaco so the editor does not
      // consume Ctrl+L as its default line-selection command.
      editorInstance.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyL, () => {
        emit('askAgent', getSelectedText())
      })

      editorInstance.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyK, () => openInlineEdit())

      editorInstance.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.KeyP, () => {
        emit('openCommandPalette', 'command')
      })

      // Cursor position and selection listener
      editorInstance.onDidChangeCursorPosition((e) => {
        const sel = editorInstance?.getSelection()
        let selectedChars = 0
        if (sel && editorInstance?.getModel()) {
          const model = editorInstance.getModel()!
          const text = model.getValueInRange(sel)
          selectedChars = text.length
        }

        emit('cursorChange', {
          line: e.position.lineNumber,
          column: e.position.column,
          selectedChars,
        } as CursorPositionInfo)
      })

      // Selection change -> Floating Quick Action bar
      editorInstance.onDidChangeCursorSelection((e) => {
        if (inlineEdit.value.visible) return
        const selection = e.selection
        if (!selection || selection.isEmpty()) {
          selectionBar.value.visible = false
          return
        }

        const model = editorInstance?.getModel()
        if (!model) return

        const selectedText = model.getValueInRange(selection).trim()
        if (selectedText.length > 3) {
          const coords = editorInstance?.getScrolledVisiblePosition(selection.getEndPosition())
          if (coords && editorContainerRef.value) {
            const rect = editorContainerRef.value.getBoundingClientRect()
            selectionBar.value = {
              visible: true,
              x: Math.min(rect.width - 240, Math.max(20, coords.left)),
              y: Math.max(10, coords.top - 36),
              selectedText,
            }
          }
        } else {
          selectionBar.value.visible = false
        }
      })

      // Resize observer
      if (window.ResizeObserver && editorContainerRef.value) {
        resizeObserver = new ResizeObserver(() => {
          editorInstance?.layout()
        })
        resizeObserver.observe(editorContainerRef.value)
      }
    }

    // Switch or update model when tab changes
    const applyTabToEditor = () => {
      if (!editorInstance) return
      const tab = props.tab

      if (!tab) {
        editorInstance.setModel(null)
        selectionBar.value.visible = false
        closeInlineEdit()
        return
      }

      isUpdatingModel = true
      let model = tab.model

      if (!model || model.isDisposed()) {
        const uri = monaco.Uri.parse(`file:///${tab.path.replace(/\\/g, '/')}`)
        const existingModel = monaco.editor.getModel(uri)
        if (existingModel && !existingModel.isDisposed()) {
          model = existingModel
        } else {
          model = monaco.editor.createModel(tab.content, getMonacoLanguage(tab.name), uri)
        }
        tab.model = model

        // Listen for content changes
        model.onDidChangeContent(() => {
          if (isUpdatingModel) return
          const val = model!.getValue()
          tab.content = val
          tab.isDirty = val !== tab.originalContent
        })
      }

      editorInstance.setModel(model)
      if (tab.viewState) {
        editorInstance.restoreViewState(tab.viewState)
      }
      editorInstance.focus()
      isUpdatingModel = false
    }

    watch(() => props.tab?.path, (newPath, oldPath) => {
      if (editorInstance && oldPath && props.tab) {
        // Save viewState before switching
        props.tab.viewState = editorInstance.saveViewState()
      }
      nextTick(() => {
        applyTabToEditor()
      })
    })

    watch(() => props.theme, (t) => {
      if (t) monaco.editor.setTheme(t)
    })

    watch(() => props.fontSize, (size) => {
      editorInstance?.updateOptions({ fontSize: size })
    })

    watch(() => props.wordWrap, (wrap) => {
      editorInstance?.updateOptions({ wordWrap: wrap ? 'on' : 'off' })
    })

    onMounted(() => {
      initEditor()
      if (props.tab) {
        applyTabToEditor()
      }
    })

    onUnmounted(() => {
      if (editorInstance && props.tab) {
        props.tab.viewState = editorInstance.saveViewState()
      }
      resizeObserver?.disconnect()
      editorInstance?.dispose()
      editorInstance = null
    })

    const formatCode = () => {
      editorInstance?.getAction('editor.action.formatDocument')?.run()
    }

    const revealLine = (lineNumber: number) => {
      if (!editorInstance) return
      editorInstance.revealLineInCenter(lineNumber)
      editorInstance.setPosition({ lineNumber, column: 1 })
      editorInstance.focus()
    }

    const getSelectedText = () => {
      const model = editorInstance?.getModel()
      const selection = editorInstance?.getSelection()
      if (!model || !selection || selection.isEmpty()) return ''
      return model.getValueInRange(selection).trim()
    }

    const clearInlineDecoration = () => {
      if (editorInstance && inlineDecorations.length) {
        inlineDecorations = editorInstance.deltaDecorations(inlineDecorations, [])
      }
    }

    const closeInlineEdit = () => {
      clearInlineDecoration()
      inlineEdit.value = {
        visible: false, x: 16, y: 48, instruction: '', loading: false,
        error: '', replacement: null, summary: '', range: null, originalText: '',
      }
      editorInstance?.focus()
    }

    const openInlineEdit = () => {
      const model = editorInstance?.getModel()
      const selection = editorInstance?.getSelection()
      if (!model || !selection || !props.tab) return

      const range = selection.isEmpty()
        ? new monaco.Range(selection.startLineNumber, 1, selection.startLineNumber, model.getLineMaxColumn(selection.startLineNumber))
        : new monaco.Range(selection.startLineNumber, selection.startColumn, selection.endLineNumber, selection.endColumn)
      const coords = editorInstance?.getScrolledVisiblePosition(range.getStartPosition())
      const rect = editorContainerRef.value?.getBoundingClientRect()
      const width = rect?.width || 700
      const height = rect?.height || 500
      clearInlineDecoration()
      inlineDecorations = editorInstance!.deltaDecorations([], [{
        range,
        options: { inlineClassName: 'agentgo-inline-target', stickiness: monaco.editor.TrackedRangeStickiness.NeverGrowsWhenTypingAtEdges },
      }])
      inlineEdit.value = {
        visible: true,
        x: Math.max(12, Math.min(width - 530, coords?.left || 16)),
        y: Math.max(12, Math.min(height - 320, (coords?.top || 24) + 24)),
        instruction: '',
        loading: false,
        error: '',
        replacement: null,
        summary: '',
        range,
        originalText: model.getValueInRange(range),
      }
      selectionBar.value.visible = false
      nextTick(() => inlineInputRef.value?.focus())
    }

    const submitInlineEdit = async () => {
      const state = inlineEdit.value
      const model = editorInstance?.getModel()
      if (!model || !state.range || !props.tab || state.loading) return
      const instruction = state.instruction.trim()
      if (!instruction) {
        state.error = '请先输入修改要求'
        return
      }
      if (model.getValueInRange(state.range) !== state.originalText) {
        state.error = '目标代码已经变化，请取消后重新选择'
        return
      }

      const startOffset = model.getOffsetAt(state.range.getStartPosition())
      const endOffset = model.getOffsetAt(state.range.getEndPosition())
      const fullText = model.getValue()
      const contextBefore = fullText.slice(Math.max(0, startOffset - 4000), startOffset)
      const contextAfter = fullText.slice(endOffset, Math.min(fullText.length, endOffset + 4000))
      state.loading = true
      state.error = ''
      state.replacement = null
      try {
        const res = await wailsCall<{
          success?: boolean
          error?: string
          replacement?: string
          summary?: string
        }>(
          'GenerateInlineEdit',
          props.tab.path,
          props.tab.language,
          state.originalText,
          contextBefore,
          contextAfter,
          instruction,
        )
        if (res?.success === false || res?.error || typeof res?.replacement !== 'string') {
          state.error = res?.error || '模型没有返回可用的修改'
          return
        }
        state.replacement = res.replacement
        state.summary = res.summary || '已生成候选修改'
      } catch (e: any) {
        state.error = e?.message || '生成内联修改失败'
      } finally {
        state.loading = false
      }
    }

    const applyInlineEdit = () => {
      const state = inlineEdit.value
      const model = editorInstance?.getModel()
      if (!editorInstance || !model || !state.range || state.replacement === null) return
      if (model.getValueInRange(state.range) !== state.originalText) {
        state.error = '目标代码已经变化，请取消后重新选择'
        return
      }
      editorInstance.pushUndoStop()
      editorInstance.executeEdits('agentgo-inline-edit', [{ range: state.range, text: state.replacement, forceMoveMarkers: true }])
      editorInstance.pushUndoStop()
      const startOffset = model.getOffsetAt(state.range.getStartPosition())
      const endPosition = model.getPositionAt(startOffset + state.replacement.length)
      editorInstance.setSelection(new monaco.Range(
        state.range.startLineNumber,
        state.range.startColumn,
        endPosition.lineNumber,
        endPosition.column,
      ))
      closeInlineEdit()
    }

    return {
      editorContainerRef,
      inlineInputRef,
      inlineEdit,
      selectionBar,
      formatCode,
      revealLine,
      getSelectedText,
      openInlineEdit,
      closeInlineEdit,
      submitInlineEdit,
      applyInlineEdit,
      askAgent: () => {
        emit('askAgent', selectionBar.value.selectedText)
        selectionBar.value.visible = false
      },
      explainCode: () => {
        emit('explainCode', selectionBar.value.selectedText)
        selectionBar.value.visible = false
      },
      refactorCode: openInlineEdit,
    }
  },
  render() {
    return (
      <div style={{ position: 'relative', width: '100%', height: '100%', overflow: 'hidden' }}>
        <div
          ref="editorContainerRef"
          style={{
            width: '100%',
            height: '100%',
            backgroundColor: 'var(--bg-editor, #18181B)',
          }}
        />

        {this.inlineEdit.visible && (
          <div
            style={{
              position: 'absolute',
              top: `${this.inlineEdit.y}px`,
              left: `${this.inlineEdit.x}px`,
              width: 'min(510px, calc(100% - 24px))',
              backgroundColor: '#202024',
              border: '1px solid #A78BFA',
              borderRadius: '9px',
              boxShadow: '0 12px 36px rgba(0,0,0,0.55)',
              zIndex: 80,
              maxHeight: 'calc(100% - 24px)',
              overflowY: 'auto',
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '8px 10px', borderBottom: '1px solid rgba(255,255,255,0.08)' }}>
              <span style={{ color: '#C4B5FD', fontWeight: 700, fontSize: '12px', whiteSpace: 'nowrap' }}>⌘K 内联编辑</span>
              <input
                ref="inlineInputRef"
                value={this.inlineEdit.instruction}
                disabled={this.inlineEdit.loading}
                onInput={(e) => { this.inlineEdit.instruction = (e.target as HTMLInputElement).value }}
                onKeydown={(e) => {
                  if (e.key === 'Escape') {
                    e.preventDefault()
                    this.closeInlineEdit()
                  } else if (e.key === 'Enter') {
                    e.preventDefault()
                    if ((e.ctrlKey || e.metaKey) && this.inlineEdit.replacement !== null) {
                      this.applyInlineEdit()
                    } else {
                      this.submitInlineEdit()
                    }
                  }
                }}
                placeholder="描述要怎样修改，Enter 生成，Ctrl+Enter 接受…"
                style={{ flex: 1, minWidth: 0, border: 'none', outline: 'none', background: 'transparent', color: '#F4F4F5', fontSize: '13px' }}
              />
              <button onClick={this.closeInlineEdit} title="取消 (Esc)" style={{ border: 'none', background: 'transparent', color: '#71717A', cursor: 'pointer', fontSize: '15px' }}>×</button>
            </div>

            <div style={{ padding: '9px 10px' }}>
              {this.inlineEdit.loading ? (
                <div style={{ color: '#A1A1AA', fontSize: '12px' }}>正在生成候选修改…</div>
              ) : this.inlineEdit.error ? (
                <div style={{ color: '#F87171', fontSize: '12px', lineHeight: 1.5 }}>{this.inlineEdit.error}</div>
              ) : this.inlineEdit.replacement !== null ? (
                <div>
                  <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '7px' }}>
                    <span style={{ color: '#C4B5FD', fontSize: '11px' }}>{this.inlineEdit.summary}</span>
                    <span style={{ color: '#71717A', fontSize: '10px' }}>Ctrl+Enter 接受 | Esc 放弃</span>
                  </div>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '6px' }}>
                    <div style={{ minWidth: 0 }}>
                      <div style={{ color: '#FCA5A5', fontSize: '10px', marginBottom: '3px' }}>修改前</div>
                      <pre style={{ margin: 0, maxHeight: '160px', overflow: 'auto', padding: '7px', borderRadius: '5px', background: 'rgba(127,29,29,0.16)', color: '#D4D4D8', fontSize: '10.5px', lineHeight: 1.5, whiteSpace: 'pre-wrap', fontFamily: "'Fira Code', Consolas, monospace" }}>
                        {this.inlineEdit.originalText || '（空）'}
                      </pre>
                    </div>
                    <div style={{ minWidth: 0 }}>
                      <div style={{ color: '#86EFAC', fontSize: '10px', marginBottom: '3px' }}>修改后</div>
                      <pre style={{ margin: 0, maxHeight: '160px', overflow: 'auto', padding: '7px', borderRadius: '5px', background: 'rgba(20,83,45,0.16)', color: '#D4D4D8', fontSize: '10.5px', lineHeight: 1.5, whiteSpace: 'pre-wrap', fontFamily: "'Fira Code', Consolas, monospace" }}>
                        {this.inlineEdit.replacement || '（删除选中内容）'}
                      </pre>
                    </div>
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '6px', marginTop: '8px' }}>
                    <button onClick={this.closeInlineEdit} style={{ padding: '4px 9px', borderRadius: '4px', border: '1px solid rgba(255,255,255,0.13)', background: 'transparent', color: '#A1A1AA', cursor: 'pointer', fontSize: '11px' }}>取消 (Esc)</button>
                    <button onClick={this.submitInlineEdit} style={{ padding: '4px 9px', borderRadius: '4px', border: '1px solid rgba(255,255,255,0.13)', background: 'transparent', color: '#D4D4D8', cursor: 'pointer', fontSize: '11px' }}>重新生成</button>
                    <button onClick={this.applyInlineEdit} style={{ padding: '4px 11px', borderRadius: '4px', border: 'none', background: '#A78BFA', color: '#18181B', cursor: 'pointer', fontWeight: 700, fontSize: '11px' }}>接受修改 (Ctrl+↵)</button>
                  </div>
                </div>
              ) : (
                <div style={{ color: '#71717A', fontSize: '11px' }}>
                  {this.inlineEdit.originalText.length} 个字符已作为修改目标；未点击“应用”前不会改变代码。
                </div>
              )}
            </div>
          </div>
        )}

        {/* Floating Quick Action Bar for selected code */}
        {this.selectionBar.visible && (
          <div
            style={{
              position: 'absolute',
              top: `${this.selectionBar.y}px`,
              left: `${this.selectionBar.x}px`,
              display: 'flex',
              alignItems: 'center',
              gap: '4px',
              padding: '3px 6px',
              backgroundColor: '#27272A',
              border: '1px solid var(--accent, #38BDF8)',
              borderRadius: '6px',
              boxShadow: '0 4px 14px rgba(0,0,0,0.4)',
              zIndex: 50,
              fontSize: '11px',
              userSelect: 'none',
              animation: 'fadeIn 0.15s ease-out',
            }}
          >
            <button
              onClick={this.askAgent}
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: '4px',
                padding: '2px 7px',
                background: 'var(--accent, #38BDF8)',
                color: '#09090B',
                border: 'none',
                borderRadius: '4px',
                fontWeight: 600,
                cursor: 'pointer',
              }}
            >
              <span>✨ Agent 提问</span>
            </button>
            <button
              onClick={this.explainCode}
              style={{
                padding: '2px 7px',
                background: 'rgba(255,255,255,0.08)',
                color: '#E4E4E7',
                border: 'none',
                borderRadius: '4px',
                cursor: 'pointer',
              }}
            >
              解释
            </button>
            <button
              onClick={this.refactorCode}
              style={{
                padding: '2px 7px',
                background: 'rgba(255,255,255,0.08)',
                color: '#E4E4E7',
                border: 'none',
                borderRadius: '4px',
                cursor: 'pointer',
              }}
            >
              重构
            </button>
          </div>
        )}
      </div>
    )
  },
})
