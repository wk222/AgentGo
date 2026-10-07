import { defineComponent, type PropType } from 'vue'
import { Workspace } from './workspace'

export const CodeWorkspacePanel = defineComponent({
  name: 'CodeWorkspacePanel',
  props: {
    onSendToAgent: { type: Function as PropType<(prompt: string) => void>, default: undefined },
    sessionId: { type: String, default: '' },
  },
  setup(props) {
    const send = (prompt: string) => props.onSendToAgent?.(prompt)
    return () => (
      <div style={{ width: '100%', height: '100%', overflow: 'hidden' }}>
        <Workspace
          sessionId={props.sessionId}
          onAskAgent={send}
          onExplainCode={send}
          onRefactorCode={send}
        />
      </div>
    )
  },
})

export default CodeWorkspacePanel
