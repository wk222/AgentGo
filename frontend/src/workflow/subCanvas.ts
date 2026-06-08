import type { FlowgramDocument } from './flowgramAdapter'

export function emptySubCanvas(): FlowgramDocument {
  return {
    nodes: [
      { id: 'sub_start', type: 'Start', meta: { position: { x: 40, y: 80 } }, data: {} },
      { id: 'sub_end', type: 'End', meta: { position: { x: 280, y: 80 } }, data: {} },
    ],
    edges: [{ sourceNodeID: 'sub_start', targetNodeID: 'sub_end' }],
  }
}

export function parseSubCanvas(raw?: string): FlowgramDocument {
  if (!raw?.trim()) return emptySubCanvas()
  try {
    const doc = JSON.parse(raw) as FlowgramDocument
    if (doc?.nodes?.length) return doc
  } catch { /* fallthrough */ }
  return emptySubCanvas()
}

export function stringifySubCanvas(doc: FlowgramDocument): string {
  return JSON.stringify(doc)
}
