export type HarnessId = 'minimal' | 'standard' | 'ultimate' | 'creative'

export interface HarnessDef {
  id: HarnessId
  name: string
  label: string
  desc: string
  profile: string
  canvas: string
}

export const HARNESSES: readonly HarnessDef[] = [
  { id: 'minimal', name: '极简', label: '极简 Minimal', desc: '目标明确的问答与单步任务，直接动手', profile: 'assistant', canvas: 'focused' },
  { id: 'standard', name: '标准', label: '标准 Standard', desc: '日常多步工程任务：规划 → 执行 → 验证', profile: 'assistant', canvas: 'balanced' },
  { id: 'ultimate', name: '极致', label: '极致 Ultimate', desc: '复杂任务深度拆解、全量工具协同与闭环自检', profile: 'admin', canvas: 'deep' },
  { id: 'creative', name: '创造', label: '创造 Creative', desc: '构建、预览与迭代 Inner App 及工作流', profile: 'app_matrix', canvas: 'balanced' },
]

export function getHarness(id: HarnessId): HarnessDef {
  return HARNESSES.find(h => h.id === id) ?? HARNESSES[1]
}

export function harnessOf(profile: string, canvas: string): HarnessDef {
  if (profile === 'app_matrix' || profile === 'matrix') return getHarness('creative')
  if (profile === 'admin' || canvas === 'deep') return getHarness('ultimate')
  if (canvas === 'focused') return getHarness('minimal')
  return getHarness('standard')
}
