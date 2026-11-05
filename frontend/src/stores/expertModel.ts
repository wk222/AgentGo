// 专家（对话的角色）的纯展示逻辑：分组、标签、切换后的提示语。
// 不碰 Vue 与 IPC，单测见 scripts/expert-model.test.cjs。
import type { ExpertInfo, SessionExpertReply } from '../shared/ipc/types'

/** 分类的显示顺序，与后端 experts.categoryOrder 一致；用户自己写的分类排在后面，按字母序。 */
export const CATEGORY_ORDER = ['office', 'code', 'research', 'automation', 'agentgo', 'writing', 'general']

const CATEGORY_LABELS: Record<string, string> = {
  office: '办公',
  code: '编程',
  research: '研究',
  automation: '自动化',
  agentgo: 'AgentGo',
  writing: '写作',
  general: '通用',
}

/** 分类的中文名；不认识的分类（用户自己起的）原样显示。 */
export function categoryLabel(category: string | null | undefined): string {
  const key = String(category || '').trim()
  if (!key) return CATEGORY_LABELS.general
  return CATEGORY_LABELS[key] || key
}

export interface ExpertGroup {
  category: string
  label: string
  experts: ExpertInfo[]
}

/** 按分类分组。组内保持后端给的顺序（序号，再按 id），所以这里不再排。 */
export function groupExperts(list: ExpertInfo[] | null | undefined): ExpertGroup[] {
  const byCat = new Map<string, ExpertInfo[]>()
  for (const e of list || []) {
    if (!e || !String(e.id || '').trim()) continue
    const key = String(e.category || '').trim() || 'general'
    const bucket = byCat.get(key)
    if (bucket) bucket.push(e)
    else byCat.set(key, [e])
  }
  const known = CATEGORY_ORDER.filter(c => byCat.has(c))
  const custom = Array.from(byCat.keys()).filter(c => !CATEGORY_ORDER.includes(c)).sort((a, b) => a.localeCompare(b))
  return [...known, ...custom].map(category => ({ category, label: categoryLabel(category), experts: byCat.get(category) || [] }))
}

/** 来源的小标签：官方的不标，用户自己的和工作区里的各一个词。 */
export function sourceLabel(e: Pick<ExpertInfo, 'source'> | null | undefined): string {
  if (e?.source === 'user') return '我的'
  if (e?.source === 'workspace') return '工作区'
  return ''
}

/** 推荐技能里当前用不上的（没装、被关掉）：只用来在悬浮提示里说一声。 */
export function missingSkills(e: Pick<ExpertInfo, 'skills'> | null | undefined): string[] {
  return (e?.skills || []).filter(s => s && s.available === false).map(s => s.id)
}

/** 卡片的悬浮提示：一句话介绍 + 来源/覆盖关系 + 缺的技能。 */
export function expertTooltip(e: ExpertInfo): string {
  const lines = [String(e.summary || '').trim()]
  const src = sourceLabel(e)
  if (src) lines.push(e.overrides ? `${src}专家，盖掉了同名的${e.overrides === 'official' ? '内置' : '我的'}专家` : `${src}专家`)
  const miss = missingSkills(e)
  if (miss.length) lines.push(`当前没有这些技能：${miss.join('、')}`)
  return lines.filter(Boolean).join('\n')
}

export type ExpertStateKind = 'none' | 'active' | 'missing'

export interface ExpertState {
  kind: ExpertStateKind
  /** 绑定的专家 id；没有则空串。 */
  id: string
  /** 能显示的名字：专家还在时是它的名字，已删除时退回 id。 */
  name: string
  expert: ExpertInfo | null
}

/** GetSessionExpert / SetSessionExpert 的回答 → 现在这条对话的专家状态。 */
export function expertState(r: SessionExpertReply | null | undefined): ExpertState {
  const id = String(r?.expert_id || '').trim()
  if (!id) return { kind: 'none', id: '', name: '', expert: null }
  const expert = r?.expert || null
  if (!expert || r?.missing) return { kind: 'missing', id, name: id, expert: null }
  return { kind: 'active', id, name: String(expert.name || '').trim() || id, expert }
}

/** 标题栏/卡片上的短标签。已删除的专家说清楚，免得用户以为还在生效。 */
export function expertChip(s: ExpertState): string {
  if (s.kind === 'none') return ''
  return s.kind === 'missing' ? `${s.id}（已删除）` : s.name
}

/** 绑定处于「已删除」时给用户的解释：对话不会坏，只是没有专家。 */
export const MISSING_EXPERT_HINT = '这位专家的文件已经不在了，这条对话现在按普通对话工作。可以换一位，或取消。'

export interface ExpertNotice {
  tone: 'success' | 'info' | 'error'
  text: string
}

/** 切换/取消专家之后对用户说什么。成功时点明「从下一条消息起生效」：已经发出的不会被改。 */
export function expertNotice(r: (SessionExpertReply & { success?: boolean; error?: string }) | null | undefined, hadMessages: boolean): ExpertNotice {
  if (!r || !r.success) return { tone: 'error', text: String(r?.error || '').trim() || '设置专家失败' }
  const st = expertState(r)
  const later = hadMessages ? '；从下一条消息起生效' : ''
  if (st.kind === 'none') return { tone: 'info', text: `已取消专家${later}` }
  return { tone: 'success', text: `已切换为「${st.name}」${later}` }
}

/** 当前专家的示例提示，去掉空白条目，最多 max 条。 */
export function pickStarters(e: Pick<ExpertInfo, 'starters'> | null | undefined, max = 6): string[] {
  return (e?.starters || []).map(s => String(s || '').trim()).filter(Boolean).slice(0, max)
}

/** 用户自己放专家文件的位置说明；两个目录都没有就不说。 */
export function folderHint(userDir: string | null | undefined, workspaceDir: string | null | undefined): string {
  const parts: string[] = []
  if (userDir) parts.push(`我的专家：${userDir}`)
  if (workspaceDir) parts.push(`工作区专家：${workspaceDir}`)
  return parts.join('\n')
}
