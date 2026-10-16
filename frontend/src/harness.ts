import { shallowRef } from 'vue'

// Harness presets are defined by the backend (internal/agent/harness.go) and loaded at
// startup with loadHarnesses(). DEFAULT_HARNESSES is only the offline / browser-preview
// fallback so the UI is usable before (or without) the backend answering; keep it in step
// with the backend list, a Go test pins the ids.
export type HarnessId = string

export interface HarnessDef {
  id: HarnessId
  name: string
  label: string
  desc: string
  profile: string
  canvas: string
}

export const DEFAULT_HARNESSES: readonly HarnessDef[] = [
  { id: 'minimal', name: '极简', label: '极简 Minimal', desc: '目标明确的问答与单步任务，直接动手', profile: 'assistant', canvas: 'focused' },
  { id: 'standard', name: '标准', label: '标准 Standard', desc: '日常多步工程任务：规划 → 执行 → 验证', profile: 'assistant', canvas: 'balanced' },
  { id: 'ultimate', name: '极致', label: '极致 Ultimate', desc: '复杂任务深度拆解、全量工具协同与闭环自检', profile: 'admin', canvas: 'deep' },
  { id: 'creative', name: '创造', label: '创造 Creative', desc: '构建、预览与迭代 Inner App 及工作流', profile: 'app_matrix', canvas: 'balanced' },
]

const store = shallowRef<readonly HarnessDef[]>(DEFAULT_HARNESSES)
let defaultId = 'standard'

/** Current presets in display order. Reactive: read it inside render/computed. */
export function harnessList(): readonly HarnessDef[] {
  return store.value
}

/** A preset by id; unknown ids fall back to the default preset. */
export function getHarness(id: HarnessId): HarnessDef {
  const list = store.value
  return list.find(h => h.id === id) ?? list.find(h => h.id === defaultId) ?? list[0]
}

/**
 * The preset whose profile and canvas equal the given pair, or undefined for a
 * combination that is not a preset (set through environment variables or by an older
 * version). The caller shows the raw pair then; guessing a "nearest" preset is the
 * backend's job (SessionMode.Harness), not something to re-implement here.
 */
export function harnessOf(profile: string, canvas: string): HarnessDef | undefined {
  return store.value.find(h => h.profile === profile && h.canvas === canvas)
}

/** Label for status displays: the preset label, or the raw "profile · canvas". */
export function harnessLabel(profile: string, canvas: string): string {
  return harnessOf(profile, canvas)?.label ?? `${profile} · ${canvas}`
}

/**
 * Replace the presets with the backend's. `call` is injected (wailsCall) so this module
 * does not import the IPC layer, which itself needs DEFAULT_HARNESSES for preview mode.
 */
export async function loadHarnesses(call: (method: string) => Promise<any>): Promise<void> {
  try {
    const r = await call('ListHarnesses')
    const list = Array.isArray(r?.harnesses) ? (r.harnesses as HarnessDef[]).filter(h => h?.id && h.profile && h.canvas) : []
    if (list.length === 0) return
    store.value = list
    if (typeof r.default === 'string' && list.some(h => h.id === r.default)) defaultId = r.default
  } catch {
    // keep the fallback list
  }
}
