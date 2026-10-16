// 由 wails.ts 机械拆分而来（见 docs/plan.md F6）。
import { logPreviewOnce } from '../runtime'
import { emitMock } from './events'
import { MAX_PREVIEW_BYTES, MockSession, PREVIEW_GLOBAL_MODE_KEY, PREVIEW_LLM_KEY, PREVIEW_WORKSPACE_ROOT_KEY, clone, defaultMode, ensureSession, getMockSessions, getSessionMode, modeKey, nowISO, publicSessions, readJSON, saveMockSessions, writeJSON } from './state'
import { bytesOf, cleanRel, mockFiles, mockWorkspace, mockWorkspaceInfo, mockWorkspaceRoot } from './workspace'
import { findMockApp, getMockApps, getMockMemories, mockAppManifest, mockCapabilityGrants, mockInnerAppHTML, mockTermInput, mockTerminals, saveCustomMockApp, saveMockMemories } from './data'
import { mockEngine, mockStream, nextPreviewSeq, previewFlowgram, previewRuns } from './stream'
import { DEFAULT_HARNESSES } from '../../harness'

let mockReviewActive = false
let mockReviewChanges: any[] = []
let mockReviewBefore: Record<string, string> = {}

export async function mockCall(method: string, ...params: any[]): Promise<any> {
  logPreviewOnce()
  switch (method) {
    case 'ListSessions':
      return publicSessions()
    case 'NewSession': {
      const title = String(params[0] || '新对话')
      const session: MockSession = { id: `preview-${Date.now()}`, title, updated_at: nowISO(), message_count: 0, messages: [] }
      const sessions = getMockSessions()
      sessions.unshift(session)
      saveMockSessions(sessions)
      return { success: true, id: session.id, session }
    }
    case 'DeleteSession': {
      const sid = String(params[0] || '')
      saveMockSessions(getMockSessions().filter(s => s.id !== sid))
      return { success: true, id: sid }
    }
    case 'GetSessionMessages': {
      const sid = String(params[0] || '')
      const session = ensureSession(sid)
      return { session_id: sid, messages: clone(session.messages), count: session.messages.length }
    }
    case 'SendMessageStream':
      return mockStream(String(params[0] || ''), String(params[1] || ''))
    case 'RunEngine':
      return mockEngine(String(params[1] || ''), String(params[2] || ''))
    case 'CancelEngineSession':
    case 'CancelEngineRun': {
      for (const [sid, run] of previewRuns) {
        if (sid !== params[0] && run.runID !== params[0]) continue
        run.timers.forEach(clearTimeout)
        emitMock('engine:event', { run_id: run.runID, session_id: sid, seq: nextPreviewSeq(),
          type: 'done', payload: { messages: [], pending: false, error: 'cancelled' } })
        previewRuns.delete(sid)
      }
      return { success: true, preview: true }
    }
    case 'ResolveApproval': {
      const run = [...previewRuns.values()].find(r => r.approvalID === params[0])
      if (!run) return { success: false, error: '预览审批已处理或不存在' }
      const sessions = getMockSessions()
      const row = sessions.find(s => s.id === run.sid)?.messages.find(m => m.approval_id === params[0])
      if (row) { row.status = params[1] ? 'approved' : 'rejected'; row.resolved = true; saveMockSessions(sessions) }
      run.approvalID = undefined
      if (params[1]) mockEngine(run.sid, '', true)
      else {
        emitMock('engine:event', { run_id: run.runID, session_id: run.sid, seq: nextPreviewSeq(),
          type: 'done', payload: { messages: [], pending: false, error: '' } })
        previewRuns.delete(run.sid)
      }
      return { success: true, preview: true }
    }
    case 'StopSession':
    case 'CancelA2UIInteraction':
    case 'ResolveA2UIInteraction':
      return { success: true, preview: true }
    case 'AnswerQuestion':
      return { success: true, content: '预览模式已记录你的回答，真实桌面运行时会继续恢复 Agent。' }

    case 'GetFeatureFlags':
      return { success: true, mode_profile: defaultMode().profile, execution_canvas: defaultMode().canvas, a2ui: true, preview: true }
    case 'ListHarnesses':
      return { success: true, harnesses: DEFAULT_HARNESSES, default: 'standard', preview: true }
    case 'GetSessionModeForSession':
      return { success: true, ...getSessionMode(String(params[0] || '')) }
    case 'SetSessionModeForSession': {
      const sid = String(params[0] || '')
      const mode = { profile: String(params[1] || 'assistant'), canvas: String(params[2] || 'balanced') }
      writeJSON(modeKey(sid), mode)
      return { success: true, ...mode }
    }
    case 'SetSessionMode': {
      const mode = { profile: String(params[0] || 'assistant'), canvas: String(params[1] || 'balanced') }
      writeJSON(PREVIEW_GLOBAL_MODE_KEY, mode)
      return { success: true, ...mode }
    }

    case 'GetLLMConfig':
      return readJSON(PREVIEW_LLM_KEY, { api_base: 'https://api.openai.com/v1', api_key: '', model: 'gpt-4o', fallback_model: '', has_azure_luna: true, azure_api_base: 'https://azure.openai.mock/openai/v1/', azure_model: 'gpt-6-luna', has_deepseek: true })
    case 'LoadAzureLunaConfig':
      writeJSON(PREVIEW_LLM_KEY, { api_base: 'https://azure.openai.mock/openai/v1/', api_key: 'mock-azure-luna-key', model: 'gpt-6-luna', fallback_model: '' })
      return { success: true, model: 'gpt-6-luna', api_base: 'https://azure.openai.mock/openai/v1/' }
    case 'LoadDeepSeekConfig': {
      const m = String(params[0] || 'deepseek-reasoner')
      const fb = m === 'deepseek-chat' ? 'deepseek-reasoner' : 'deepseek-chat'
      writeJSON(PREVIEW_LLM_KEY, { api_base: 'https://api.deepseek.com/v1', api_key: 'mock-deepseek-key', model: m, fallback_model: fb, has_deepseek: true })
      return { success: true, model: m, api_base: 'https://api.deepseek.com/v1', api_key_set: true, from_env_key: true }
    }
    case 'SetLLMConfig': {
      const cfg = { api_base: params[0] || '', api_key: params[1] || '', model: params[2] || '', fallback_model: params[3] || '' }
      writeJSON(PREVIEW_LLM_KEY, cfg)
      return { success: true, preview: true }
    }
    case 'TestLLM':
      return { ok: true, message: 'localhost preview runtime 可用' }

    case 'GetWorkspaceInfo':
      return mockWorkspaceInfo()
    case 'SetWorkspaceRoot': {
      const root = String(params[0] || '').trim()
      if (!root) return { success: false, error: 'workspace root is empty', preview: true }
      writeJSON(PREVIEW_WORKSPACE_ROOT_KEY, root)
      return mockWorkspaceInfo(root)
    }
    case 'ListWorkspace':
    case 'ListWorkspaceDir': {
      const rel = cleanRel(params[0])
      return clone(mockWorkspace[rel] || [])
    }
    case 'ReadWorkspaceFile': {
      const rel = cleanRel(params[0])
      const entry = Object.values(mockWorkspace).flat().find((x: any) => x.path === rel)
      if (entry?.size > MAX_PREVIEW_BYTES) {
        return { success: false, error: `文件超过预览限制 (${Math.round(MAX_PREVIEW_BYTES / 1024)} KB)`, size: entry.size }
      }
      const content = mockFiles[rel]
      if (content == null) return { success: false, error: 'mock 文件不存在' }
      return { success: true, content, size: bytesOf(content) }
    }
    case 'WriteWorkspaceFile': {
      const rel = cleanRel(params[0])
      const content = String(params[1] ?? '')
      mockFiles[rel] = content
      return { success: true, path: rel, size: bytesOf(content), preview: true }
    }
    case 'CreateWorkspaceFile':
    case 'CreateWorkspaceDir': {
      const rel = cleanRel(params[0])
      if (!rel) return { success: false, error: '路径不能为空', preview: true }
      const parts = rel.split('/')
      const name = parts.pop() || rel
      const parent = parts.join('/')
      const entries = ((mockWorkspace as Record<string, any[]>)[parent] ||= [])
      if (entries.some((entry: any) => entry.path === rel)) {
        return { success: false, error: '文件或文件夹已存在', preview: true }
      }
      const isDir = method === 'CreateWorkspaceDir'
      entries.push({ name, path: rel, is_dir: isDir, size: 0, mod_time: nowISO(), git_status: '??' })
      if (isDir) (mockWorkspace as Record<string, any[]>)[rel] = []
      else mockFiles[rel] = ''
      return { success: true, path: rel, preview: true }
    }
    case 'DeleteWorkspacePath': {
      const rel = cleanRel(params[0])
      for (const entries of Object.values(mockWorkspace) as any[][]) {
        const idx = entries.findIndex((entry: any) => entry.path === rel)
        if (idx >= 0) entries.splice(idx, 1)
      }
      delete mockFiles[rel]
      delete (mockWorkspace as Record<string, any[]>)[rel]
      return { success: true, path: rel, preview: true }
    }
    case 'RenameWorkspacePath': {
      const oldRel = cleanRel(params[0])
      const newRel = cleanRel(params[1])
      const newParts = newRel.split('/')
      const newName = newParts.pop() || newRel
      let moved: any = null
      for (const entries of Object.values(mockWorkspace) as any[][]) {
        const idx = entries.findIndex((entry: any) => entry.path === oldRel)
        if (idx >= 0) moved = entries.splice(idx, 1)[0]
      }
      if (!moved) return { success: false, error: '原路径不存在', preview: true }
      moved = { ...moved, name: newName, path: newRel, mod_time: nowISO() }
      const parent = newParts.join('/')
      ;((mockWorkspace as Record<string, any[]>)[parent] ||= []).push(moved)
      if (oldRel in mockFiles) {
        mockFiles[newRel] = mockFiles[oldRel]
        delete mockFiles[oldRel]
      }
      if ((mockWorkspace as Record<string, any[]>)[oldRel]) {
        ;(mockWorkspace as Record<string, any[]>)[newRel] = (mockWorkspace as Record<string, any[]>)[oldRel]
        delete (mockWorkspace as Record<string, any[]>)[oldRel]
      }
      return { success: true, path: newRel, preview: true }
    }
    case 'ExecuteTerminalCommand': {
      const command = String(params[0] || '')
      if (command.includes('git branch --show-current')) {
        return { success: true, stdout: 'main\n', stderr: '', exit_code: 0, preview: true }
      }
      return { success: true, stdout: `preview> ${command}\n`, stderr: '', exit_code: 0, preview: true }
    }
    case 'TerminalCreate': {
      const id = String(params[0] || `term-${Date.now()}`)
      const shell = String(params[1] || 'pwsh')
      const cwd = String(params[2] || mockWorkspaceRoot())
      const cols = Number(params[3]) || 120
      const rows = Number(params[4]) || 30
      const info = {
        id,
        title: shell.includes('pwsh') ? 'PowerShell' : (shell.includes('bash') ? 'Bash' : 'Terminal'),
        pid: Math.floor(Math.random() * 90000) + 1000,
        shell,
        cwd,
        created_at: nowISO(),
      }
      mockTerminals.set(id, { info, cols, rows })
      setTimeout(() => {
        emitMock('terminal:data', {
          term_id: id,
          data: `\x1b[32mAgentGo Interactive Terminal (Mock Preview)\x1b[0m\r\n\x1b[90mShell: ${shell} | CWD: ${cwd}\x1b[0m\r\n\r\n\x1b[36m${cwd} > \x1b[0m`,
        })
      }, 50)
      return info
    }
    case 'TerminalWrite': {
      const id = String(params[0] || '')
      const data = String(params[1] || '')
      if (data === '\r' || data === '\n') {
        const line = mockTermInput.get(id) || ''
        mockTermInput.set(id, '')
        emitMock('terminal:data', { term_id: id, data: '\r\n' })
        if (line.trim() === 'clear') {
          emitMock('terminal:data', { term_id: id, data: '\x1b[2J\x1b[3J\x1b[H\x1b[36mpreview > \x1b[0m' })
        } else if (line.trim() === 'ls' || line.trim() === 'dir') {
          emitMock('terminal:data', {
            term_id: id,
            data: `README.md  package.json  src/  docs/\r\n\x1b[36mpreview > \x1b[0m`,
          })
        } else if (line.trim()) {
          emitMock('terminal:data', {
            term_id: id,
            data: `[preview mock exec]: ${line}\r\n\x1b[36mpreview > \x1b[0m`,
          })
        } else {
          emitMock('terminal:data', { term_id: id, data: '\x1b[36mpreview > \x1b[0m' })
        }
      } else if (data === '\x03') {
        mockTermInput.set(id, '')
        emitMock('terminal:data', { term_id: id, data: '^C\r\n\x1b[36mpreview > \x1b[0m' })
      } else if (data === '\x7f' || data === '\b') {
        const cur = mockTermInput.get(id) || ''
        if (cur.length > 0) {
          mockTermInput.set(id, cur.slice(0, -1))
          emitMock('terminal:data', { term_id: id, data: '\b \b' })
        }
      } else {
        const cur = mockTermInput.get(id) || ''
        mockTermInput.set(id, cur + data)
        emitMock('terminal:data', { term_id: id, data })
      }
      return { success: true }
    }
    case 'TerminalResize': {
      return { success: true }
    }
    case 'TerminalClose': {
      const id = String(params[0] || '')
      mockTerminals.delete(id)
      mockTermInput.delete(id)
      emitMock('terminal:exit', { term_id: id, exit_code: 0 })
      return { success: true }
    }
    case 'TerminalList': {
      return Array.from(mockTerminals.values()).map(t => t.info)
    }
    case 'TerminalPoll': {
      return ''
    }
    case 'SearchFiles': {
      const pattern = String(params[0] || '').toLowerCase()
      const maxResults = Number(params[1]) || 20
      const all = Object.keys(mockFiles)
      const matched = pattern ? all.filter(p => p.toLowerCase().includes(pattern)) : all
      return matched.slice(0, maxResults)
    }
    case 'WorkspaceGitStatus':
      return {
        success: true,
        branch: 'main',
        staged: [],
        unstaged: [{ path: 'README.md', name: 'README.md', status: 'M', staged: false }],
        untracked: [],
        total: 1,
        preview: true,
      }
    case 'WorkspaceGitBranches':
      return { success: true, current: 'main', branches: ['main'], preview: true }
    case 'SteerSession': {
      const text = String(params[1] || '')
      return { success: true, steered: true, instruction: text, preview: true }
    }
    case 'WorkspaceDiagnostics':
      return {
        success: true,
        total: 0,
        servers: {
          mock_lsp: { state: 'ready', diagnostic_count: 0, diagnostics: [] },
        },
        preview: true,
      }
    case 'WorkspaceGitStage':
    case 'WorkspaceGitUnstage':
    case 'WorkspaceGitCommit':
      return { success: true, preview: true }
    case 'WorkspaceFileDiff': {
      const rel = cleanRel(params[0])
      const diff = rel
        ? `diff --git a/${rel} b/${rel}
--- a/${rel}
+++ b/${rel}
@@ -1,3 +1,4 @@
 原始内容
+localhost preview: 新增 Agent 上下文入口
 保留真实 Wails v3 runtime
`
        : ''
      return {
        success: true,
        path: rel,
        diff,
        added: diff ? 1 : 0,
        removed: 0,
        untracked: false,
        has_changes: !!diff,
        original_content: mockFiles[rel] || '',
        modified_content: `${mockFiles[rel] || ''}\n\n<!-- localhost preview change -->`,
      }
    }

    case 'BeginWorkspaceReview': {
      if (!mockReviewActive || mockReviewChanges.length === 0) {
        mockReviewActive = true
        mockReviewBefore = { 'README.md': mockFiles['README.md'] || '' }
        mockFiles['README.md'] = `${mockReviewBefore['README.md']}\n\n> localhost preview: Agent 建议新增这一行。`
        mockReviewChanges = [{
          path: 'README.md', name: 'README.md', status: 'modified', reviewable: true,
          binary: false, before_size: bytesOf(mockReviewBefore['README.md']), after_size: bytesOf(mockFiles['README.md']),
        }]
      }
      return { success: true, resumed: mockReviewChanges.length > 0, pending_count: mockReviewChanges.length, skipped_count: 0, preview: true }
    }
    case 'ListWorkspaceReview':
      return { success: true, active: mockReviewActive, changes: clone(mockReviewChanges), skipped_count: 0, preview: true }
    case 'WorkspaceReviewFile': {
      const rel = cleanRel(params[1])
      const change = mockReviewChanges.find((item: any) => item.path === rel)
      if (!change) return { success: false, error: 'change not found', preview: true }
      return { success: true, change, original_content: mockReviewBefore[rel] || '', modified_content: mockFiles[rel] || '', preview: true }
    }
    case 'AcceptWorkspaceReviewFile': {
      const rel = cleanRel(params[1])
      mockReviewChanges = mockReviewChanges.filter((item: any) => item.path !== rel)
      if (mockReviewChanges.length === 0) mockReviewActive = false
      return { success: true, remaining: mockReviewChanges.length, preview: true }
    }
    case 'RejectWorkspaceReviewFile': {
      const rel = cleanRel(params[1])
      if (rel in mockReviewBefore) mockFiles[rel] = mockReviewBefore[rel]
      mockReviewChanges = mockReviewChanges.filter((item: any) => item.path !== rel)
      if (mockReviewChanges.length === 0) mockReviewActive = false
      return { success: true, remaining: mockReviewChanges.length, preview: true }
    }
    case 'AcceptAllWorkspaceReview':
      mockReviewChanges = []
      mockReviewActive = false
      return { success: true, remaining: 0, preview: true }
    case 'RejectAllWorkspaceReview':
      for (const [path, content] of Object.entries(mockReviewBefore)) mockFiles[path] = content
      mockReviewChanges = []
      mockReviewActive = false
      return { success: true, remaining: 0, preview: true }
    case 'GenerateInlineEdit': {
      const selected = String(params[2] ?? '')
      const instruction = String(params[5] || '').trim()
      const replacement = instruction.includes('标题')
        ? '# AgentGo · AI IDE'
        : `${selected}\n// localhost preview: ${instruction}`
      return { success: true, replacement, summary: `预览修改：${instruction || '优化选中代码'}`, preview: true }
    }

    case 'ListMemories':
      return getMockMemories().filter((m: any) => m.status === 'active').slice(0, Number(params[0] || 40))
    case 'MemoryFeedback': {
      const id = String(params[0] || '')
      const signal = String(params[1] || '')
      const items = getMockMemories().map((m: any) => {
        if (m.id !== id) return m
        if (signal === 'disproved') return { ...m, status: 'forgotten', updated_at: Date.now() / 1000 }
        const delta = signal === 'positive' ? 0.15 : signal === 'negative' ? -0.15 : 0
        return { ...m, importance: Math.max(0.1, Math.min(2, Number(m.importance || 1) + delta)), updated_at: Date.now() / 1000 }
      })
      saveMockMemories(items)
      return { success: true, preview: true }
    }
    case 'MemoryGC':
      return { success: true, archived: 1, preview: true }
    case 'MemoryDistill':
    case 'MemoryDistillLLM':
      return { success: true, summary: '预览蒸馏：保留用户对 Eino 复用、UI 可维护面板、Workspace 上下文入口的偏好。', preview: true }
    case 'MemoryContextPrompt':
      return {
        success: true,
        scope: params[0] || 'global',
        prompt: '- 用户偏好：优先复用 Eino 和 Wails v3 真实能力。\n- 当前关注：Agent Context、InnerApp 独立窗口、Memory 可维护面板。',
        preview: true,
      }

    case 'MemoryRecallQA': {
      const q = String(params[0] || 'session context user preferences goals')
      const scope = String(params[1] || 'global')
      const modality = String(params[2] || '')
      const limit = Number(params[3] || 8)
      const rows = getMockMemories()
        .filter((m: any) => m.status === 'active')
        .filter((m: any) => !scope || scope === 'global' || m.scope === scope || m.scope === 'global' || (scope.startsWith('preview') && m.scope === 'session'))
        .filter((m: any) => !modality || m.modality === modality)
        .slice(0, limit)
        .map((m: any, i: number) => ({
          ...m,
          rank: i + 1,
          estimated_score: Number((0.85 - i * 0.07).toFixed(3)),
          source: 'mock_hybrid_recall',
          why: `rank #${i + 1}; scope=${m.scope}; modality=${m.modality}; preview runtime`,
        }))
      return {
        success: true,
        query: q,
        scope,
        modality,
        limit,
        elapsed_ms: 12,
        count: rows.length,
        records: rows,
        engine: 'localhost-preview',
        prompt: rows.map((r: any) => `- [${r.modality}] ${r.content}`).join('\n'),
        prompt_bytes: rows.reduce((n: number, r: any) => n + String(r.content || '').length, 0),
        preview: true,
      }
    }

    case 'DetectMemoryContradictions': {
      const newFact = String(params[0] || '')
      const scope = String(params[1] || 'global')
      const candidate = getMockMemories()
        .find((m: any) => m.status === 'active' && (!scope || scope === 'global' || m.scope === scope || m.scope === 'global'))
      return candidate ? [{
        existing_id: candidate.id,
        explanation: `preview candidate for "${newFact.slice(0, 40)}"`,
      }] : []
    }
    case 'ResolveMemoryTruth':
      return {
        resolution: 'supersede_a_with_b',
        explanation: 'localhost preview: treat Fact B as newer/canonical',
      }
    case 'ApplyMemoryTruthResolution': {
      const factAID = String(params[0] || '')
      const factBID = String(params[1] || '')
      let resolution: any = {}
      try { resolution = JSON.parse(String(params[2] || '{}')) } catch {}
      const items = getMockMemories()
      let applied: any = null
      const next = items.map((m: any) => {
        if (resolution.resolution === 'supersede_a_with_b' && m.id === factAID) {
          return { ...m, status: 'archived', supersedes_id: factBID, contradicted_by: factBID, updated_at: Date.now() / 1000 }
        }
        if (resolution.resolution === 'supersede_a_with_b' && m.id === factBID) {
          applied = { ...m, is_canonical: true, updated_at: Date.now() / 1000 }
          return applied
        }
        if (resolution.resolution === 'supersede_b_with_a' && m.id === factBID) {
          return { ...m, status: 'archived', supersedes_id: factAID, contradicted_by: factAID, updated_at: Date.now() / 1000 }
        }
        if (resolution.resolution === 'supersede_b_with_a' && m.id === factAID) {
          applied = { ...m, is_canonical: true, updated_at: Date.now() / 1000 }
          return applied
        }
        return m
      })
      if (resolution.resolution === 'merge') {
        applied = {
          id: `truth-merge-${Date.now()}`,
          content: resolution.merged_content || 'Preview merged memory',
          scope: 'global',
          modality: 'fact',
          status: 'active',
          importance: 1.4,
          recall_count: 0,
          is_canonical: true,
          source_trust: 1,
          created_at: Date.now() / 1000,
          updated_at: Date.now() / 1000,
        }
        next.unshift(applied)
      }
      saveMockMemories(next)
      resolution.applied_record_id = applied?.id || factBID || factAID
      return { success: true, resolution, record: applied || next.find((m: any) => m.id === resolution.applied_record_id), preview: true }
    }
    case 'ResolveAndApplyMemoryTruth': {
      const res = await mockCall('ResolveMemoryTruth', params[0], params[1])
      return mockCall('ApplyMemoryTruthResolution', params[0], params[1], JSON.stringify(res))
    }
    case 'GetMemoryGraph': {
      const center = String(params[0] || '')
      const rows = getMockMemories().filter((m: any) => m.status === 'active')
      const centerRow = rows.find((m: any) => m.id === center) || rows[0]
      const neighbors = rows.filter((m: any) => m.id !== centerRow?.id).slice(0, 3)
      return {
        success: !!centerRow,
        graph: {
          nodes: centerRow ? [centerRow, ...neighbors] : [],
          edges: centerRow ? neighbors.map((n: any, i: number) => ({
            source_id: centerRow.id,
            target_id: n.id,
            relation: i === 0 ? 'related' : 'supports',
            weight: 1 - i * 0.1,
          })) : [],
        },
        preview: true,
      }
    }

    case 'ListSkills':
      return [
        { id: 'skill:code_review', name: 'code_review', description: '检查行为回归、风险和测试缺口。', scope: 'project', path: 'skills/code_review.md' },
        { id: 'skill:frontend_qa', name: 'frontend_qa', description: '做响应式布局和交互状态 QA。', scope: 'workspace', path: 'skills/frontend_qa.md' },
        { id: 'skill:eino_architecture', name: 'eino_architecture', description: '优先复用 Eino compose、tool 和 callback 能力。', scope: 'project', path: 'skills/eino_architecture.md' },
      ]
    case 'GetSkillContext':
      return `# Skill Context

- 使用 Eino/compose 能力减少自研调度代码。
- UI QA 时检查 topbar、A2UI 卡片和 Workflow 编辑器在右栏打开时的布局。`
    case 'ListRegisteredTools':
      return ['list_workspace', 'read_file', 'workspace_file_diff', 'remember', 'invoke_inner_app', 'run_workflow']
    case 'Capabilities': {
      return [
        { id: 'chat.eino', title: 'Eino agent chat', available: true, plugins: ['eino'] },
        { id: 'code.tools', title: 'Code-aware tools (LSP, edit, search)', available: true, plugins: ['codetools'] },
        { id: 'tools.builtin', title: 'Built-in agent tools', available: true, plugins: ['toolset'] },
        { id: 'workflow', title: 'Workflows', available: true, plugins: ['workflow'] },
        { id: 'approvals', title: 'Human approval of risky actions', available: true, plugins: ['approvals'] },
        { id: 'memory', title: 'Long-term memory', available: true, plugins: ['memory'] },
        { id: 'ide', title: 'Workspace files, terminal and git', available: true, plugins: ['ide'] },
        { id: 'host.attach', title: 'Attach other terminals to this process', available: true, plugins: ['host'] },
      ]
    }
    case 'ListCapabilityGrants': {
      const kind = String(params[0] || '')
      const rows = mockCapabilityGrants()
      return kind ? rows.filter(g => g.kind === kind) : rows
    }
    case 'ListCapabilities': {
      const kind = String(params[0] || '')
      const rows = mockCapabilityGrants()
      return (kind ? rows.filter(g => g.kind === kind) : rows).map(g => ({ id: g.id, kind: g.kind, name: g.name, scope: g.scope, metadata: g.metadata }))
    }
    case 'TransitionCapabilityGrant':
    case 'VerifyCapabilityGrant':
    case 'DeprecateCapabilityGrant':
    case 'DeleteCapabilityGrant':
      return { success: true, preview: true }

    case 'ListInnerApps':
      return getMockApps().map(app => ({ ...app, manifest: mockAppManifest(app) }))
    case 'BuildInnerAppIteratively': {
      const name = String(params[0] || '').trim() || `preview-app-${Date.now()}`
      const app = {
        id: `app:${name}`,
        name,
        icon: '▣',
        description: String(params[2] || params[1] || 'AI 生成的预览 Inner App'),
        enabled: true,
        kind: 'ui',
        has_ui: true,
        bundle_path: name,
      }
      saveCustomMockApp(app)
      return {
        success: true,
        app_name: name,
        iterations: 1,
        message: 'localhost preview 已生成 UI bundle',
        final_verify: { success: true, score: 92, max_score: 100, message: 'preview verification passed' },
      }
    }
    case 'GetInnerAppInfo': {
      const app = getMockApps().find(a => a.name === params[0] || a.id === params[0])
      return app ? { success: true, ...app, manifest: mockAppManifest(app), files: ['index.html', 'static/app.js', 'static/style.css'] } : { success: false, error: 'app not found' }
    }
    case 'GetInnerAppManifest': {
      const app = findMockApp(params[0])
      return app ? { success: true, manifest: mockAppManifest(app) } : { success: false, error: 'app not found' }
    }
    case 'OpenInnerAppSession': {
      const app = findMockApp(params[0]) || { name: params[0] || 'inner-app', kind: 'ui', enabled: true, has_ui: true }
      return {
        success: true,
        session_id: `preview-inner-${Date.now()}`,
        nonce: Math.random().toString(36).slice(2),
        created_at: Math.floor(Date.now() / 1000),
        expires_at: Math.floor(Date.now() / 1000) + 7200,
        ttl_sec: 7200,
        manifest: mockAppManifest(app),
        preview: true,
      }
    }
    case 'GetInnerAppPageHTML':
      return { success: true, html: mockInnerAppHTML(String(params[0] || 'inner-app')), app: params[0] || 'inner-app' }
    case 'OpenInnerAppWindow':
      return { success: false, error: 'localhost preview 使用内嵌预览，Wails 桌面版会打开独立窗口。', preview: true }
    case 'ReadInnerAppFile':
      return { success: true, text: '', mime: 'text/plain; charset=utf-8' }
    case 'InvokeInnerApp':
      return { success: true, content: `预览模式已模拟调用 ${params[0] || 'app'}` }
    case 'CallInnerAppAPI':
      return { success: true, output: `预览模式已调用 ${params[0] || 'app'}.${params[1] || 'action'}`, payload: params[2] || '{}' }

    case 'CallInnerAppSessionAPI':
      return { success: true, output: `preview called ${params[0] || 'app'}.${params[3] || 'action'}`, session_id: params[1] || '', payload: params[4] || '{}', preview: true }

    case 'ListBackgroundTasks':
      return [
        { id: 'task-ui-qa', title: 'UI 视觉 QA', goal: '检查窄屏与右栏打开状态', status: 'running', steps: 4 },
        { id: 'task-eino-gap', title: 'Eino 差距梳理', goal: '减少自研组件', status: 'completed', steps: 6 },
      ]
    case 'ListScheduledJobs':
      return [{ id: 'job-memory-gc', title: 'Memory GC', spec: 'daily 03:00', status: 'active' }]
    case 'ListChannels':
      return [
        { id: 'local', channel: 'Local Desktop', kind: 'desktop', state: 'connected' },
        { id: 'preview', channel: 'Vite Preview', kind: 'browser', state: 'active' },
      ]

    case 'GetConfigSnapshot':
      return [
        { name: 'AGENTGO_DEBUG_LLM', kind: 'bool', default: 'false', group: 'debug', doc: '预览数据：打印 LLM 请求', value: 'false', set: false },
      ]

    case 'ListWorkflows':
      return [{ id: 'wf_preview_happy_path', name: '预览 Happy Path', description: 'Start → Tool → End' }]
    case 'GetWorkflowFlowgram':
      return { success: true, flowgram: clone(previewFlowgram) }
    case 'SaveWorkflowFlowgram':
    case 'SaveWorkflow':
      return { success: true, preview: true }
    case 'RunWorkflow':
      return { success: true, output: '预览工作流运行完成。' }
    case 'EnsureWorkflowHappyPath':
      return { success: true, id: 'wf_preview_happy_path', message: '预览演示工作流已就绪。' }
    case 'OpenWorkflowWindow':
      return { success: false, error: 'localhost preview 使用页内弹出编辑器，Wails 桌面版会打开独立窗口。', preview: true }

    default:
      console.warn('[AgentGo] mock runtime 未实现方法:', method, params)
      return { success: true, preview: true }
  }
}
