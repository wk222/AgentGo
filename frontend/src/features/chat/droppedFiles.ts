// Turning a file dropped from the OS file manager into something the agent can open.
//
// A browser (and WebView2) only gives the page a File: name, size, modified time,
// never its location. The agent needs a path, so we ask the local server to find
// the real file, and only when that fails do we hand it a copy in the workspace.
import { wailsCall, waitForWailsEvents } from '../../shared/wails'

/** Event the desktop host emits with the real paths of files dropped on the chat. */
export const FILES_DROPPED_EVENT = 'files:dropped'

export interface NativeDroppedPath {
  path: string
  is_dir?: boolean
}

/**
 * True inside the desktop window, whose host resolves real paths itself (the
 * Wails runtime swallows the drop and posts the paths to Go). In a browser it is
 * false and the page has to work the path out from the File.
 */
export function nativeFileDropEnabled(): boolean {
  const w = window as any
  return w._wails?.flags?.enableFileDrop === true && w.chrome?.webview?.postMessageWithAdditionalObjects != null
}

const IMAGE_EXT = /\.(png|jpe?g|gif|webp|bmp)$/i

/** Images keep going through the image-attachment path, not the file chips. */
export function isImagePath(path: string): boolean {
  return IMAGE_EXT.test(path)
}

/** Listen for paths the desktop host resolved. Returns the unsubscribe function. */
export async function watchNativeFileDrops(onPaths: (items: NativeDroppedPath[]) => void): Promise<() => void> {
  const events = await waitForWailsEvents(3000)
  if (!events?.On) return () => {}
  const off = events.On(FILES_DROPPED_EVENT, (e: any) => {
    const body = e && typeof e === 'object' && e.name === FILES_DROPPED_EVENT && 'data' in e ? e.data : e
    const list = Array.isArray(body?.files) ? body.files : []
    const items = list.filter((f: any) => f && typeof f.path === 'string' && f.path.trim() !== '')
    if (items.length > 0) onPaths(items)
  })
  return typeof off === 'function' ? off : () => {}
}

export type DropOutcome = 'path' | 'found' | 'copied'

export interface LocatedFile {
  path: string
  outcome: DropOutcome
}

/** Same ceiling as the server's ImportChatFile; larger files must be given by path. */
export const MAX_COPY_BYTES = 40 * 1024 * 1024

export function isAbsolutePath(p: string): boolean {
  return /^[a-zA-Z]:[\\/]/.test(p) || p.startsWith('\\\\') || p.startsWith('/')
}

/** The @mention for a path; a name with a space is quoted so the backend reads it whole. */
export function mentionTag(path: string): string {
  return /\s/.test(path) ? `@"${path}"` : `@${path}`
}

function toBase64(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf)
  const step = 0x8000
  let bin = ''
  for (let i = 0; i < bytes.length; i += step) {
    bin += String.fromCharCode.apply(null, Array.from(bytes.subarray(i, i + step)))
  }
  return btoa(bin)
}

/**
 * Resolve a dropped file to a path the agent can use, in order of preference:
 *   1. the path the host already gave us (some shells expose File.path),
 *   2. the one file on this PC with the same name, size and modified time,
 *   3. a copy placed under the workspace's uploads/ folder.
 * Throws with a user-readable message when none of them works.
 */
export async function locateDroppedFile(file: File & { path?: string }): Promise<LocatedFile> {
  if (file.path && isAbsolutePath(file.path)) return { path: file.path, outcome: 'path' }

  try {
    const found = await wailsCall('ResolveDroppedFile', file.name, file.size, Math.round(file.lastModified || 0))
    if (found?.success && found.path) return { path: found.path, outcome: 'found' }
  } catch {
    // fall through to the copy: finding the original is only an optimisation
  }

  if (file.size > MAX_COPY_BYTES) {
    throw new Error(`「${file.name}」超过 ${MAX_COPY_BYTES / 1024 / 1024} MB 且没能找到它的原位置，请在输入框直接写它的完整路径`)
  }
  const res = await wailsCall('ImportChatFile', file.name, toBase64(await file.arrayBuffer()))
  if (!res?.success || !res.path) throw new Error(res?.error || `「${file.name}」导入失败`)
  return { path: res.path, outcome: 'copied' }
}

/** What to tell the user once a file is attached, so nobody edits a copy by mistake. */
export function dropNotice(name: string, located: LocatedFile): { level: 'success' | 'info'; text: string } {
  if (located.outcome === 'copied') {
    return {
      level: 'info',
      text: `没找到「${name}」在电脑上的原位置，已复制到工作区 ${located.path}；修改只会作用于这份副本`,
    }
  }
  return { level: 'success', text: `已关联 ${located.path.replace(/\//g, '\\')}` }
}
