import { defineComponent, ref, onMounted, onUnmounted, watch, type PropType } from 'vue'
import * as monaco from 'monaco-editor'
import { setupMonaco, getMonacoLanguage } from '../../../utils/monaco'

export const DiffViewer = defineComponent({
  name: 'DiffViewer',
  props: {
    filePath: { type: String, required: true },
    originalContent: { type: String, default: '' },
    modifiedContent: { type: String, default: '' },
    originalTitle: { type: String, default: 'HEAD (原始)' },
    modifiedTitle: { type: String, default: '工作区 (修改后)' },
    theme: { type: String, default: 'agentgo-dark' },
  },
  emits: ['close', 'revert'],
  setup(props, { emit }) {
    const diffContainerRef = ref<HTMLDivElement | null>(null)
    const renderSideBySide = ref(true)
    let diffEditorInstance: monaco.editor.IStandaloneDiffEditor | null = null
    let resizeObserver: ResizeObserver | null = null
    let originalModel: monaco.editor.ITextModel | null = null
    let modifiedModel: monaco.editor.ITextModel | null = null

    const initDiffEditor = () => {
      if (!diffContainerRef.value) return
      setupMonaco()

      diffEditorInstance = monaco.editor.createDiffEditor(diffContainerRef.value, {
        theme: props.theme,
        renderSideBySide: renderSideBySide.value,
        readOnly: true,
        automaticLayout: true,
        fontSize: 13,
        fontFamily: "'Fira Code', 'Cascadia Code', 'JetBrains Mono', 'Consolas', monospace",
        minimap: { enabled: false },
        scrollBeyondLastLine: false,
      })

      updateModels()

      if (window.ResizeObserver && diffContainerRef.value) {
        resizeObserver = new ResizeObserver(() => {
          diffEditorInstance?.layout()
        })
        resizeObserver.observe(diffContainerRef.value)
      }
    }

    const updateModels = () => {
      if (!diffEditorInstance) return

      if (originalModel) originalModel.dispose()
      if (modifiedModel) modifiedModel.dispose()

      const lang = getMonacoLanguage(props.filePath)
      originalModel = monaco.editor.createModel(props.originalContent, lang)
      modifiedModel = monaco.editor.createModel(props.modifiedContent, lang)

      diffEditorInstance.setModel({
        original: originalModel,
        modified: modifiedModel,
      })
    }

    watch(() => [props.originalContent, props.modifiedContent, props.filePath], () => {
      updateModels()
    })

    watch(() => renderSideBySide.value, (val) => {
      diffEditorInstance?.updateOptions({ renderSideBySide: val })
    })

    watch(() => props.theme, (t) => {
      if (t) monaco.editor.setTheme(t)
    })

    onMounted(() => {
      initDiffEditor()
    })

    onUnmounted(() => {
      resizeObserver?.disconnect()
      if (originalModel) originalModel.dispose()
      if (modifiedModel) modifiedModel.dispose()
      diffEditorInstance?.dispose()
      diffEditorInstance = null
    })

    return {
      diffContainerRef,
      renderSideBySide,
    }
  },
  render() {
    return (
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          width: '100%',
          height: '100%',
          backgroundColor: '#18181B',
          overflow: 'hidden',
        }}
      >
        {/* Top Diff Action Bar */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '6px 14px',
            backgroundColor: '#1E1E22',
            borderBottom: '1px solid rgba(255, 255, 255, 0.08)',
            fontSize: '12px',
            color: '#A1A1AA',
            userSelect: 'none',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
            <span style={{ fontWeight: 600, color: '#FFFFFF' }}>差异比对: {this.filePath}</span>
            <span style={{ fontSize: '11px', color: '#71717A' }}>
              ({this.originalTitle} ⟷ {this.modifiedTitle})
            </span>
          </div>

          <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
            {/* Toggle Inline / Side-by-side */}
            <button
              onClick={() => { this.renderSideBySide = !this.renderSideBySide }}
              style={{
                padding: '2px 8px',
                backgroundColor: 'rgba(255, 255, 255, 0.08)',
                color: '#E4E4E7',
                border: 'none',
                borderRadius: '4px',
                cursor: 'pointer',
                fontSize: '11px',
              }}
            >
              {this.renderSideBySide ? '切换至内联 (Inline)' : '切换至分栏 (Side-by-side)'}
            </button>

            {/* Close Diff View */}
            <button
              onClick={() => this.$emit('close')}
              style={{
                padding: '2px 8px',
                backgroundColor: 'transparent',
                color: '#A1A1AA',
                border: '1px solid rgba(255, 255, 255, 0.1)',
                borderRadius: '4px',
                cursor: 'pointer',
                fontSize: '11px',
              }}
            >
              关闭比对
            </button>
          </div>
        </div>

        {/* Diff Container */}
        <div ref="diffContainerRef" style={{ flex: 1, width: '100%' }} />
      </div>
    )
  },
})
