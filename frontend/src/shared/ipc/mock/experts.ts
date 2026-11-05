// 浏览器预览用的专家样例：形状与后端 ListExperts / GetSessionExpert 一致，只放几位，
// 够把选择、切换、取消、已删除几种状态点出来。真实列表在桌面应用里读文件。
import type { ExpertInfo } from '../types'

export const MOCK_EXPERTS: ExpertInfo[] = [
  {
    id: 'financial-modeler', name: '财务建模师', category: 'office', source: 'official', order: 40,
    summary: '搭能复算的财务模型：假设单独成表，公式引用单元格而不是写死数字。',
    skills: [{ id: 'office-financial-model', available: true }, { id: 'office-xlsx', available: true }],
    starters: ['按这份历史数据搭一个三年的收入与现金流预测模型', '检查这个模型里有没有写死的数字和断开的引用'],
  },
  {
    id: 'pitch-deck-designer', name: '路演稿设计师', category: 'office', source: 'official', order: 50,
    summary: '把想法排成一份讲得清楚的路演稿：一页一个观点，数字有出处。',
    skills: [{ id: 'office-pitch-deck', available: true }],
    starters: ['为一个面向企业的 SaaS 产品做 10 页融资路演稿'],
  },
  {
    id: 'software-engineer', name: '软件工程师', category: 'code', source: 'official', order: 10,
    summary: '先读懂再动手：小步修改、改完跑测试，不顺手重构无关代码。',
    skills: [],
    starters: ['读一下这个项目的结构，告诉我入口在哪、数据怎么流动'],
  },
  {
    id: 'research-analyst', name: '研究分析师', category: 'research', source: 'official', order: 10,
    summary: '带着引用做调研：每个结论说明来自哪里，查不到就直说。',
    skills: [],
    starters: ['调研一下这个领域近两年的主要方案，并列出各自的取舍'],
  },
  {
    id: 'weekly-report', name: '周报助手', category: 'writing', source: 'user', order: 100,
    summary: '（预览样例）我自己写的专家：把零散记录整理成固定格式的周报。',
    skills: [],
    starters: ['把下面的记录整理成本周周报：'],
  },
]

/** 按 id 或名字找一位专家，和后端 Catalog.Get 一样大小写不敏感地比名字。 */
export function findMockExpert(key: string): ExpertInfo | undefined {
  const k = String(key || '').trim()
  if (!k) return undefined
  return MOCK_EXPERTS.find(e => e.id === k) || MOCK_EXPERTS.find(e => e.name.toLowerCase() === k.toLowerCase())
}

export function mockExpertNotFound(key: string): string {
  const brief = MOCK_EXPERTS.map(e => `${e.id}（${e.name}）`).join('、')
  return String(key || '').trim() ? `没有叫 "${key}" 的专家。现有专家：${brief}` : '没有指定专家'
}
