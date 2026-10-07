import type { OutlineSymbol } from '../types'

export function parseOutline(content: string, language: string): OutlineSymbol[] {
  if (!content) return []
  const lines = content.split(/\r?\n/)
  const symbols: OutlineSymbol[] = []

  switch (language) {
    case 'typescript':
    case 'javascript':
    case 'typescriptreact':
    case 'javascriptreact': {
      lines.forEach((line, index) => {
        const lineNum = index + 1
        const trimmed = line.trim()

        // Class / Interface / Type / Enum
        const classMatch = trimmed.match(/^(?:export\s+)?(?:default\s+)?class\s+([A-Za-z0-9_$]+)/)
        if (classMatch) {
          symbols.push({ name: classMatch[1], kind: 'class', line: lineNum })
          return
        }
        const ifaceMatch = trimmed.match(/^(?:export\s+)?interface\s+([A-Za-z0-9_$]+)/)
        if (ifaceMatch) {
          symbols.push({ name: ifaceMatch[1], kind: 'interface', line: lineNum })
          return
        }
        const typeMatch = trimmed.match(/^(?:export\s+)?type\s+([A-Za-z0-9_$]+)\s*=/)
        if (typeMatch) {
          symbols.push({ name: typeMatch[1], kind: 'interface', line: lineNum })
          return
        }
        const enumMatch = trimmed.match(/^(?:export\s+)?enum\s+([A-Za-z0-9_$]+)/)
        if (enumMatch) {
          symbols.push({ name: enumMatch[1], kind: 'enum', line: lineNum })
          return
        }

        // Functions & Component definitions
        const funcMatch = trimmed.match(/^(?:export\s+)?(?:async\s+)?function\s+([A-Za-z0-9_$]+)/)
        if (funcMatch) {
          symbols.push({ name: `${funcMatch[1]}()`, kind: 'function', line: lineNum })
          return
        }
        const arrowFuncMatch = trimmed.match(/^(?:export\s+)?(?:const|let|var)\s+([A-Za-z0-9_$]+)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z0-9_$]+)\s*=>/)
        if (arrowFuncMatch) {
          symbols.push({ name: `${arrowFuncMatch[1]}()`, kind: 'function', line: lineNum })
          return
        }
        const defineCompMatch = trimmed.match(/^(?:export\s+)?(?:const|let)\s+([A-Za-z0-9_$]+)\s*=\s*defineComponent\(/)
        if (defineCompMatch) {
          symbols.push({ name: `<${defineCompMatch[1]} />`, kind: 'class', line: lineNum })
          return
        }
      })
      break
    }

    case 'go': {
      lines.forEach((line, index) => {
        const lineNum = index + 1
        const trimmed = line.trim()

        // struct / interface type
        const typeMatch = trimmed.match(/^type\s+([A-Za-z0-9_]+)\s+(struct|interface)/)
        if (typeMatch) {
          symbols.push({ name: typeMatch[1], kind: typeMatch[2] === 'struct' ? 'class' : 'interface', line: lineNum })
          return
        }

        // Methods: func (r *Receiver) MethodName(...)
        const methodMatch = trimmed.match(/^func\s+\((?:[a-zA-Z0-9_*\s]+)\)\s+([A-Za-z0-9_]+)\s*\(/)
        if (methodMatch) {
          symbols.push({ name: `${methodMatch[1]}()`, kind: 'method', line: lineNum })
          return
        }

        // Functions: func FunctionName(...)
        const funcMatch = trimmed.match(/^func\s+([A-Za-z0-9_]+)\s*\(/)
        if (funcMatch) {
          symbols.push({ name: `${funcMatch[1]}()`, kind: 'function', line: lineNum })
          return
        }
      })
      break
    }

    case 'python': {
      lines.forEach((line, index) => {
        const lineNum = index + 1
        const trimmed = line.trim()

        const classMatch = trimmed.match(/^class\s+([A-Za-z0-9_]+)/)
        if (classMatch) {
          symbols.push({ name: classMatch[1], kind: 'class', line: lineNum })
          return
        }

        const funcMatch = trimmed.match(/^(?:async\s+)?def\s+([A-Za-z0-9_]+)\s*\(/)
        if (funcMatch) {
          symbols.push({ name: `${funcMatch[1]}()`, kind: 'function', line: lineNum })
          return
        }
      })
      break
    }

    case 'markdown': {
      lines.forEach((line, index) => {
        const lineNum = index + 1
        const match = line.match(/^(#{1,6})\s+(.+)$/)
        if (match) {
          const depth = match[1].length
          symbols.push({
            name: `${'  '.repeat(depth - 1)}${match[2]}`,
            kind: 'heading',
            line: lineNum,
            detail: `H${depth}`,
          })
        }
      })
      break
    }

    default:
      break
  }

  return symbols
}
