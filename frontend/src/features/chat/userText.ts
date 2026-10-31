// 斜杠命令（如 /review）展开后的用户消息：第一行是用户敲的原话，后面跟一段
// <command-instructions> 说明书，只给模型看。对话气泡、复制、回滚到检查点只用用户敲的那部分。
const COMMAND_BLOCK = /\n*<command-instructions\b[^>]*>[\s\S]*?<\/command-instructions>/g

export function userDisplayText(content: string | null | undefined): string {
  if (!content) return ''
  if (!content.includes('<command-instructions')) return content
  return content.replace(COMMAND_BLOCK, '').trim()
}
