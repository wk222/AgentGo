import { defineComponent, type PropType } from 'vue'

export const FileIcon = defineComponent({
  name: 'FileIcon',
  props: {
    fileName: { type: String, required: true },
    isDir: { type: Boolean, default: false },
    isOpen: { type: Boolean, default: false },
    size: { type: Number, default: 15 },
  },
  setup(props) {
    return () => {
      if (props.isDir) {
        if (props.isOpen) {
          return (
            <svg width={props.size} height={props.size} viewBox="0 0 24 24" fill="none" stroke="#D97706" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style={{ flexShrink: 0 }}>
              <path d="M4 20h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.93a2 2 0 0 1-1.66-.9l-.82-1.2A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13c0 1.1.9 2 2 2Z" />
              <path d="M2 10h20" />
            </svg>
          )
        }
        return (
          <svg width={props.size} height={props.size} viewBox="0 0 24 24" fill="none" stroke="#D97706" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style={{ flexShrink: 0 }}>
            <path d="M4 20h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.93a2 2 0 0 1-1.66-.9l-.82-1.2A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13c0 1.1.9 2 2 2Z" />
          </svg>
        )
      }

      const ext = props.fileName.split('.').pop()?.toLowerCase() || ''
      const lower = props.fileName.toLowerCase()

      // Special file names
      if (lower === 'dockerfile') return <LanguageBadge label="DOCKER" color="#0284C7" bg="#E0F2FE" />
      if (lower === 'package.json') return <LanguageBadge label="NPM" color="#DC2626" bg="#FEE2E2" />
      if (lower === 'go.mod' || lower === 'go.sum') return <LanguageBadge label="GOMOD" color="#0284C7" bg="#E0F2FE" />
      if (lower === 'license' || lower === 'license.md') return <LanguageBadge label="LIC" color="#6B7280" bg="#F3F4F6" />

      switch (ext) {
        case 'go': return <LanguageBadge label="GO" color="#0284C7" bg="#E0F2FE" />
        case 'ts':
        case 'tsx': return <LanguageBadge label="TS" color="#2563EB" bg="#DBEAFE" />
        case 'js':
        case 'jsx': return <LanguageBadge label="JS" color="#CA8A04" bg="#FEF9C3" />
        case 'vue': return <LanguageBadge label="VUE" color="#059669" bg="#D1FAE5" />
        case 'py': return <LanguageBadge label="PY" color="#16A34A" bg="#DCFCE7" />
        case 'json': return <LanguageBadge label="JSON" color="#9333EA" bg="#F3E8FF" />
        case 'md':
        case 'mdx': return <LanguageBadge label="MD" color="#4B5563" bg="#F3F4F6" />
        case 'css':
        case 'scss':
        case 'less': return <LanguageBadge label="CSS" color="#EC4899" bg="#FCE7F3" />
        case 'html': return <LanguageBadge label="HTML" color="#EA580C" bg="#FFEDD5" />
        case 'sql': return <LanguageBadge label="SQL" color="#0D9488" bg="#CCFBF1" />
        case 'yaml':
        case 'yml': return <LanguageBadge label="YML" color="#DC2626" bg="#FEE2E2" />
        case 'rs': return <LanguageBadge label="RUST" color="#EA580C" bg="#FFEDD5" />
        case 'c':
        case 'cpp':
        case 'h':
        case 'hpp': return <LanguageBadge label="C++" color="#0284C7" bg="#E0F2FE" />
        case 'sh':
        case 'bash':
        case 'ps1': return <LanguageBadge label="SH" color="#059669" bg="#D1FAE5" />
        case 'png':
        case 'jpg':
        case 'jpeg':
        case 'gif':
        case 'svg':
        case 'webp':
        case 'ico': return <LanguageBadge label="IMG" color="#8B5CF6" bg="#EDE9FE" />
        default:
          return (
            <svg width={props.size} height={props.size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style={{ flexShrink: 0, opacity: 0.6 }}>
              <path d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5L14.5 2z" />
              <polyline points="14 2 14 8 20 8" />
            </svg>
          )
      }
    }
  },
})

export const LanguageBadge = ({ label, color, bg }: { label: string; color: string; bg: string }) => (
  <span
    style={{
      fontSize: '9px',
      fontWeight: 700,
      fontFamily: 'var(--font-mono, monospace)',
      color,
      backgroundColor: bg,
      padding: '1px 4px',
      borderRadius: '3px',
      lineHeight: '11px',
      flexShrink: 0,
      userSelect: 'none',
    }}
  >
    {label}
  </span>
)

export const GitStatusBadge = ({ status }: { status: string }) => {
  if (!status) return null
  let color = '#71717A'
  let bg = '#27272A'
  let label = status

  if (status === 'M' || status === 'modified') {
    color = '#F59E0B'
    bg = 'rgba(245, 158, 11, 0.15)'
    label = 'M'
  } else if (status === 'A' || status === 'added' || status === '??') {
    color = '#10B981'
    bg = 'rgba(16, 185, 129, 0.15)'
    label = status === '??' ? 'U' : 'A'
  } else if (status === 'D' || status === 'deleted') {
    color = '#EF4444'
    bg = 'rgba(239, 68, 68, 0.15)'
    label = 'D'
  }

  return (
    <span
      style={{
        fontSize: '10px',
        fontWeight: 700,
        fontFamily: 'var(--font-mono, monospace)',
        color,
        backgroundColor: bg,
        padding: '0 4px',
        borderRadius: '3px',
        lineHeight: '14px',
        marginLeft: 'auto',
        flexShrink: 0,
      }}
    >
      {label}
    </span>
  )
}
