// 记忆树视图的纯逻辑：标题、条形比例、状态提示、把「定位」返回的节点链并入已加载的树。
// 不依赖 Vue / IPC，便于直接单测（scripts/memory-tree-model.test.cjs）。

import type { MemoryTreeNode, MemoryTreeOverview } from '../../shared/ipc/types'

const MODALITY_TITLES: Record<string, string> = {
  fact: '事实', insight: '洞察', reflection: '反思', episode: '情景', journal: '日志',
  preference: '偏好', constraint: '约束', decision: '决策', lesson: '教训', unresolved: '待跟进',
}

/** 记忆类型的中文名；未知类型原样返回。 */
export function modalityTitle(modality: string): string {
  return MODALITY_TITLES[modality] || modality
}

/** 节点标题：根节点显示「记忆总览」，叶子显示记忆类型，其余沿用主题名。 */
export function nodeTitle(n: Pick<MemoryTreeNode, 'label' | 'record_id'>): string {
  if (n.label === 'Memory themes') return '记忆总览'
  return n.record_id ? modalityTitle(n.label) : (n.label || '未命名主题')
}

/** 条形长度：该节点覆盖的记忆占父节点的百分比，至少留 4% 以便看得见。 */
export function barPercent(count: number, parentCount: number): number {
  if (!(count > 0) || !(parentCount > 0)) return 4
  return Math.max(4, Math.min(100, Math.round((100 * count) / parentCount)))
}

/** 分支节点里由模型写好摘要的占比（0–100）。没有分支时为 0。 */
export function summarizedPercent(ov: Pick<MemoryTreeOverview, 'branches' | 'summarized'> | null | undefined): number {
  if (!ov || !(ov.branches > 0)) return 0
  return Math.min(100, Math.round((100 * ov.summarized) / ov.branches))
}

export interface TreeNotice {
  tone: 'ok' | 'info' | 'warn'
  text: string
}

/** 状态横幅：告诉用户这棵树此刻对模型意味着什么。 */
export function treeNotice(ov: MemoryTreeOverview | null | undefined, hierarchyOn: boolean): TreeNotice | null {
  if (!ov) return null
  if (ov.reason === 'empty') {
    return { tone: 'info', text: '这个范围里还没有记忆。对话中让智能体「记住……」，或等它自动沉淀后再来看。' }
  }
  if (!hierarchyOn) {
    return { tone: 'warn', text: '「分层摘要树」开关已关闭（在「概览」里可以打开）：模型读到的是按相关度排序的平铺记忆，这棵树只用于查看。' }
  }
  if (ov.reason === 'not_built') {
    return { tone: 'info', text: '摘要树还没建立。点「整理摘要树」生成结构；模型摘要会在后台逐步补上，期间模型按相关度直接检索。' }
  }
  if (ov.reason === 'stale') {
    return { tone: 'warn', text: '有新的记忆变动，下面是上一次整理的快照；整理完成前模型按相关度直接检索。点「整理摘要树」更新。' }
  }
  if (ov.refreshing) {
    return { tone: 'info', text: '模型正在后台为分支节点补写摘要，完成后这里会自动更新；在此之前模型读到的是子节点节选，不影响使用。若长时间没有变化，多半是模型暂时连不上。' }
  }
  if (ov.branches > 0 && ov.summarized < ov.branches) {
    return { tone: 'info', text: `已有 ${ov.summarized}/${ov.branches} 个分支由模型写好摘要，其余暂时显示子节点的节选，下次整理时会补上。` }
  }
  return { tone: 'ok', text: '摘要树是最新的，模型自动召回时按这棵树取「切面」。' }
}

/** 概览数字：已覆盖的记忆条数（树建好后才有意义）。 */
export function coverageText(ov: Pick<MemoryTreeOverview, 'covered' | 'records' | 'ready'> | null | undefined): string {
  if (!ov) return '—'
  if (!ov.ready) return `0 / ${ov.records}`
  return `${ov.covered} / ${ov.records}`
}

export interface TreeState {
  loaded: Record<string, MemoryTreeNode>
  expanded: Record<string, boolean>
}

/**
 * 把「定位」返回的节点链（根到目标，每个都带直接子节点）并入已加载的树：
 * 链上除最后一个外都展开，最后一个若是分支也一并展开。返回新对象，不改入参。
 */
export function mergeChain(state: TreeState, chain: MemoryTreeNode[]): TreeState {
  const loaded = { ...state.loaded }
  const expanded = { ...state.expanded }
  for (const node of chain) {
    loaded[node.id] = node
    if (node.children && node.children.length > 0) expanded[node.id] = true
  }
  return { loaded, expanded }
}

/** 一个节点此刻要显示的子节点：已加载的子节点列表优先于概览里附带的。 */
export function childrenOf(node: MemoryTreeNode, loaded: Record<string, MemoryTreeNode>): MemoryTreeNode[] {
  return loaded[node.id]?.children || node.children || []
}

/** 摘要 Token 占预算的比例文字，如「612 / 1000（61%）」。 */
export function budgetText(tokens: number | undefined, budget: number | undefined): string {
  const used = Math.max(0, tokens || 0)
  if (!budget || budget <= 0) return `${used}`
  return `${used} / ${budget}（${Math.min(100, Math.round((100 * used) / budget))}%）`
}
