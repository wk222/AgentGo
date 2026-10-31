// 预览模式（浏览器直接打开前端）下的用户钩子状态，形状与 Go 的 GetHooksStatus / ReloadHooks（userhooks.Status）一致。
import type { HookRun, HooksStatus } from '../types'

const minutesAgo = (m: number) => new Date(Date.now() - m * 60_000).toISOString()

const runs: HookRun[] = [
  { seq: 5, at: minutesAgo(1), event: 'Stop', hook: 'Stop python notify.py', duration_ms: 412, exit_code: 0, result: 'ok' },
  { seq: 4, at: minutesAgo(3), event: 'PreToolUse', tool: 'execute_bash', hook: 'PreToolUse[Bash] python guard.py', duration_ms: 96, exit_code: 2, result: 'blocked', reason: '不允许删除 build 目录' },
  { seq: 3, at: minutesAgo(3), event: 'UserPromptSubmit', hook: 'UserPromptSubmit python context.py', duration_ms: 1240, exit_code: 0, result: 'ok' },
  { seq: 2, at: minutesAgo(9), event: 'PreToolUse', tool: 'code_edit', hook: 'PreToolUse[Edit|Write] python lint.py', duration_ms: 30000, exit_code: -1, result: 'timeout', reason: 'timed out after 30 seconds' },
  { seq: 1, at: minutesAgo(12), event: 'PreToolUse', tool: 'execute_bash', hook: 'PreToolUse[Bash] python guard.py', duration_ms: 310, exit_code: 1, result: 'failed', reason: 'exit status 1: python: command not found' },
]

export function mockHooksStatus(): HooksStatus {
  return {
    enabled: true,
    path: 'C:\\Users\\preview\\AppData\\Local\\hooks.json',
    exists: true,
    loaded_at: minutesAgo(20),
    sha256: '7ce88e75430aa985169efcbe624932a2aa9905cbd2dd2bd723cc02de59f0bf20',
    counts: { PreToolUse: 2, PostToolUse: 0, UserPromptSubmit: 1, Stop: 1 },
    total: 4,
    hooks: [
      { event: 'PreToolUse', matcher: 'Bash', command: 'python guard.py', timeout_s: 10, on_failure: 'block' },
      { event: 'PreToolUse', matcher: 'Edit|Write', command: 'python lint.py', timeout_s: 30, on_failure: 'continue' },
      { event: 'UserPromptSubmit', command: 'python context.py', timeout_s: 30, on_failure: 'continue' },
      { event: 'Stop', command: 'python notify.py', timeout_s: 30, on_failure: 'continue' },
    ],
    problems: [{ where: 'hooks.PostToolUse[0].hooks[0].type', message: 'type "http" is not supported; only "command" is' }],
    recent: runs.map(r => ({ ...r })),
  }
}
