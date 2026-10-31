import type { ArtifactRecord } from '../../shared/ipc/types'

/** deliver_file 工具结果 / artifact 行里的交付记录；不是交付成功的结果（失败、缺字段）返回 null。 */
export function parseArtifact(content: string): ArtifactRecord | null {
  if (!content) return null
  let raw: any
  try {
    raw = JSON.parse(content)
  } catch {
    return null
  }
  if (!raw || typeof raw !== 'object' || raw.success === false) return null
  if (!raw.id || !raw.name) return null
  return {
    id: String(raw.id),
    path: String(raw.path || ''),
    name: String(raw.name),
    title: raw.title ? String(raw.title) : '',
    size: Number(raw.size) || 0,
    sha256: String(raw.sha256 || ''),
    version: Number(raw.version) || 1,
    status: ['new', 'new_version', 'unchanged', 'removed'].includes(raw.status) ? raw.status : 'new',
    snapshot: String(raw.snapshot || ''),
    url: String(raw.url || ''),
    at: String(raw.at || ''),
    attached: raw.attached !== false,
    note: raw.note ? String(raw.note) : '',
  }
}

export function artifactBadge(r: ArtifactRecord): string {
  switch (r.status) {
    case 'removed':
      return '已移除'
    case 'unchanged':
      return `第 ${r.version} 版 · 内容未变`
    case 'new_version':
      return `第 ${r.version} 版 · 已更新`
    default:
      return `第 ${r.version} 版`
  }
}

export function artifactDownloadable(r: ArtifactRecord): boolean {
  return r.status !== 'removed' && !!r.url
}

/** 用会话里的最新状态覆盖卡片上的快照（用户可能在别处移除了它）。 */
export function mergeLive(card: ArtifactRecord, live: ArtifactRecord[] | undefined): ArtifactRecord {
  const hit = (live || []).find(x => x.id === card.id)
  if (!hit) return card
  if (hit.status === 'removed') return { ...card, status: 'removed', attached: false }
  return card
}
