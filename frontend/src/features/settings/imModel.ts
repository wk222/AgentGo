// 「聊天软件」卡片的纯逻辑：渠道状态一句话、白名单文本与列表互转、提交内容的拼装。不依赖 DOM。
import type { IMChannel, IMLoginState, IMPlatform, IMSaveRequest } from '../../shared/ipc/types'

export type Tone = 'ok' | 'warn' | 'bad' | 'off'

export interface ChannelSummary { tone: Tone; text: string }

/** 一个平台当前的一句话状态。channel 为空表示还没配置。 */
export function channelSummary(platform: IMPlatform, channel: IMChannel | undefined): ChannelSummary {
  if (!channel) return { tone: 'off', text: '未配置' }
  if (channel.problems && channel.problems.length > 0) return { tone: 'bad', text: `配置有问题：${channel.problems.join('；')}` }
  if (!channel.enabled) return { tone: 'off', text: '已配置，未启用' }
  if (platform.qr_login && !channel.logged_in) return { tone: 'warn', text: '已启用，但还没有扫码登录' }
  if (!channel.allow_all && channel.allow_from.length === 0) return { tone: 'warn', text: '已启用，但没有填允许的用户：所有人都会被拒绝' }
  if (!channel.running) return { tone: 'warn', text: '已启用，尚未连上（保存后会自动重连，也可点「重新连接」）' }
  return { tone: 'ok', text: channel.allow_all ? '运行中，任何人都能使用' : `运行中，允许 ${channel.allow_from.length} 个用户/会话` }
}

/** 文本框里的白名单（逗号、分号、空白、换行分隔）转成去重后的列表。 */
export function parseAllowFrom(text: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const part of text.split(/[,;，；\s]+/)) {
    const v = part.trim()
    if (v && !seen.has(v)) { seen.add(v); out.push(v) }
  }
  return out
}

export function formatAllowFrom(list: string[] | undefined): string {
  return (list ?? []).join('\n')
}

/** 把表单拼成提交给后端的请求；密钥没填就不放进去（后端会保持原值）。 */
export function buildSaveRequest(
  platform: IMPlatform,
  form: { enabled: boolean; allowAll: boolean; allowFrom: string; values: Record<string, string> },
): IMSaveRequest {
  const fields: Record<string, string> = {}
  for (const f of platform.fields ?? []) {
    const v = (form.values[f.key] ?? '').trim()
    if (f.secret && !v) continue
    fields[f.key] = v
  }
  return { platform: platform.id, enabled: form.enabled, allow_all: form.allowAll, allow_from: parseAllowFrom(form.allowFrom), fields }
}

/** 扫码登录各阶段给用户看的话。 */
export function loginText(state: IMLoginState): string {
  if (state.message) return state.message
  switch (state.status) {
    case 'waiting': return '请用微信扫描二维码'
    case 'scanned': return '已扫码，请在手机上确认'
    case 'expired': return '二维码过期，正在换一个…'
    case 'done': return '登录成功'
    case 'error': return '登录失败'
    default: return ''
  }
}

export function loginActive(state: IMLoginState): boolean {
  return state.status === 'waiting' || state.status === 'scanned' || state.status === 'expired'
}

export const IM_EXAMPLE_NOTE = '为了安全，机器人默认拒绝所有人：只有「允许的用户」里列出的用户 ID 或会话 ID 才能使用；勾选「任何人都能用」等于把这台电脑的文件和命令执行能力开放给任何找到机器人的人。'
