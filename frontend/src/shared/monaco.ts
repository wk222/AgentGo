import * as monaco from 'monaco-editor'

let monacoInitialized = false

export const setupMonaco = () => {
  if (monacoInitialized) return
  monacoInitialized = true

  // Light theme matching Tiny-RDM / macOS clean aesthetic
  monaco.editor.defineTheme('agentgo-light', {
    base: 'vs',
    inherit: true,
    rules: [],
    colors: {
      'editor.background': '#FAFAFA',
      'editorLineNumber.foreground': '#BABBBD',
      'editorLineNumber.activeForeground': '#4A5568',
      'editor.lineHighlightBackground': '#F3F4F6',
      'editorCursor.foreground': '#2563EB',
    },
  })

  // Dark theme matching modern desktop IDEs
  monaco.editor.defineTheme('agentgo-dark', {
    base: 'vs-dark',
    inherit: true,
    rules: [],
    colors: {
      'editor.background': '#18181B',
      'editorLineNumber.foreground': '#52525B',
      'editorLineNumber.activeForeground': '#A1A1AA',
      'editor.lineHighlightBackground': '#27272A',
      'editorCursor.foreground': '#38BDF8',
    },
  })
}

export const getMonacoLanguage = (fileName: string): string => {
  const ext = fileName.split('.').pop()?.toLowerCase() || ''
  switch (ext) {
    case 'go': return 'go'
    case 'ts':
    case 'tsx': return 'typescript'
    case 'js':
    case 'jsx': return 'javascript'
    case 'py': return 'python'
    case 'json': return 'json'
    case 'md': return 'markdown'
    case 'css':
    case 'scss': return 'css'
    case 'html': return 'html'
    case 'sql': return 'sql'
    case 'yaml':
    case 'yml': return 'yaml'
    case 'xml': return 'xml'
    case 'sh':
    case 'bash':
    case 'ps1': return 'shell'
    case 'dockerfile': return 'dockerfile'
    default: return 'plaintext'
  }
}
