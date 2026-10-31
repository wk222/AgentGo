// 预览模式（浏览器直接打开前端）下的聊天软件配置，形状与 Go 的 GetIMStatus / SaveIMChannel（im.AdminStatus）一致。
import type { IMChannel, IMLoginState, IMPlatform, IMSaveRequest, IMStatus } from '../types'

const platforms: IMPlatform[] = [
  { id: 'telegram', label: 'Telegram', qr_login: false, fields: [{ key: 'bot_token', label: 'Bot Token', secret: true, hint: '在 @BotFather 创建机器人后得到' }], help: '用户 ID 可以先留空，给机器人发一条消息，它会回复你的 ID。' },
  { id: 'feishu', label: '飞书', qr_login: false, fields: [{ key: 'app_id', label: 'App ID', secret: false }, { key: 'app_secret', label: 'App Secret', secret: true }] },
  { id: 'dingtalk', label: '钉钉', qr_login: false, fields: [{ key: 'client_id', label: 'Client ID', secret: false }, { key: 'client_secret', label: 'Client Secret', secret: true }] },
  { id: 'wecom', label: '企业微信', qr_login: false, fields: [{ key: 'bot_id', label: '智能机器人 Bot ID', secret: false }, { key: 'secret', label: '智能机器人 Secret', secret: true }, { key: 'webhook_url', label: '群机器人 Webhook 地址', secret: false }, { key: 'webhook_key', label: '群机器人 Webhook Key', secret: true }] },
  { id: 'discord', label: 'Discord', qr_login: false, fields: [{ key: 'bot_token', label: 'Bot Token', secret: true }] },
  { id: 'weixin', label: '微信（iLink）', qr_login: true, fields: [], help: '不需要填密钥：点「扫码登录」，用微信扫二维码确认。' },
]

let channels: IMChannel[] = [
  { platform: 'telegram', enabled: true, running: true, allow_all: false, allow_from: ['10086'], fields: {}, secrets_set: { bot_token: true }, logged_in: false },
]

export function mockIMStatus(): IMStatus {
  return {
    config_path: 'C:\\Users\\preview\\AppData\\Local\\channels.yml',
    disabled: false,
    platforms: platforms.map(p => ({ ...p })),
    channels: channels.map(c => ({ ...c, allow_from: [...c.allow_from], fields: { ...c.fields }, secrets_set: { ...c.secrets_set } })),
  }
}

export function mockSaveIMChannel(req: IMSaveRequest): IMStatus {
  const p = platforms.find(x => x.id === req.platform)
  if (!p) throw new Error(`不支持的平台：${req.platform}`)
  const old = channels.find(c => c.platform === req.platform)
  const secrets: Record<string, boolean> = { ...(old?.secrets_set ?? {}) }
  const fields: Record<string, string> = {}
  for (const f of p.fields ?? []) {
    const v = (req.fields[f.key] ?? '').trim()
    if (f.secret) { if (v) secrets[f.key] = true } else if (v) fields[f.key] = v
  }
  if (req.enabled && p.id === 'telegram' && !secrets.bot_token) throw new Error('bot_token is required')
  const next: IMChannel = {
    platform: p.id, enabled: req.enabled, running: req.enabled, allow_all: req.allow_all,
    allow_from: req.allow_from.flatMap(s => s.split(/[,;\s]+/)).filter(Boolean),
    fields, secrets_set: secrets, logged_in: old?.logged_in ?? false,
  }
  channels = [...channels.filter(c => c.platform !== p.id), next]
  return mockIMStatus()
}

export function mockRemoveIMChannel(platform: string): IMStatus {
  channels = channels.filter(c => c.platform !== platform)
  return mockIMStatus()
}

let loginPolls = 0
export function mockStartWeixinLogin(): IMLoginState {
  loginPolls = 0
  return { status: 'waiting', message: '正在获取二维码…' }
}

export function mockWeixinLoginState(): IMLoginState {
  loginPolls++
  if (loginPolls < 3) return { status: 'waiting', message: '请用微信扫描二维码', qr_data_url: 'data:image/gif;base64,R0lGODlhAQABAAAAACw=' }
  if (loginPolls < 5) return { status: 'scanned', message: '已扫码，请在手机上确认' }
  if (!channels.some(c => c.platform === 'weixin')) {
    channels = [...channels, { platform: 'weixin', enabled: true, running: true, allow_all: false, allow_from: ['wx-preview-user'], fields: {}, secrets_set: {}, logged_in: true }]
  }
  return { status: 'done', message: '登录成功', user_id: 'wx-preview-user' }
}
