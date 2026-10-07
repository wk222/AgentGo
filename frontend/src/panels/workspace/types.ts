import type * as monaco from 'monaco-editor'

export interface WorkspaceFileItem {
  name: string
  is_dir: boolean
  path: string
  size: number
  mod_time: string
  git_status: string
  children?: WorkspaceFileItem[]
  loaded?: boolean
}

export interface OpenTab {
  id: string
  path: string
  name: string
  content: string
  originalContent: string
  isDirty: boolean
  diffMode: boolean
  diffContent: string
  language: string
  encoding: string
  eol: 'LF' | 'CRLF'
  indentSize: number
  useSpaces: boolean
  model?: monaco.editor.ITextModel
  viewState?: monaco.editor.ICodeEditorViewState | null
  isPreview?: boolean
}

export interface CursorPositionInfo {
  line: number
  column: number
  selectedChars: number
}

export type WorkspaceSidebarView = 'explorer' | 'search' | 'git' | 'outline' | 'review'
export type WorkspaceMainView = 'editor' | 'terminal' | 'diff' | 'markdown' | 'image'

export interface SearchMatch {
  line: number
  column: number
  lineText: string
  matchText: string
  length: number
}

export interface FileSearchResult {
  filePath: string
  fileName: string
  matches: SearchMatch[]
}

export interface SearchOptions {
  query: string
  replaceText: string
  isRegex: boolean
  matchCase: boolean
  matchWholeWord: boolean
  includePattern: string
  excludePattern: string
}

export interface GitStatusItem {
  path: string
  name: string
  status: 'M' | 'A' | 'D' | '??' | 'U' | string
  staged: boolean
}

export interface WorkspaceReviewChange {
  path: string
  name: string
  status: 'modified' | 'added' | 'deleted'
  reviewable: boolean
  binary: boolean
  before_size: number
  after_size: number
  reason?: string
}

export interface OutlineSymbol {
  name: string
  kind: 'class' | 'function' | 'method' | 'interface' | 'variable' | 'heading' | 'enum'
  line: number
  endLine?: number
  detail?: string
  children?: OutlineSymbol[]
}

export interface CommandItem {
  id: string
  title: string
  category?: string
  shortcut?: string
  handler: () => void | Promise<void>
}
