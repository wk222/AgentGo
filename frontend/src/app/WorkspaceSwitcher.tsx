import { defineComponent, ref, nextTick } from 'vue'
import { FolderOpen, ChevronDown, Check } from 'lucide-vue-next'
import { useWorkspace } from '../stores/useWorkspace'
import { wailsCall } from '../shared/wails'
import { notify } from '../shared/ui/notify'

// 标题栏里唯一的工作区入口：显示后端当前 workspace root；可切到最近目录或输入新路径。
export default defineComponent({
  name: 'WorkspaceSwitcher',
  setup() {
    const ws = useWorkspace()
    const open = ref(false)
    const draft = ref('')
    const browsing = ref(false)
    const input = ref<HTMLInputElement | null>(null)

    const toggle = async () => {
      open.value = !open.value
      if (open.value) {
        draft.value = ''
        await nextTick()
        input.value?.focus()
      }
    }
    const pick = async (path: string) => {
      if (!path.trim()) return
      const ok = await ws.switchTo(path)
      if (ok) {
        open.value = false
        notify.success(`工作区已切换：${ws.info.value?.root || path}`)
      } else if (ws.error.value) {
        notify.error(ws.error.value)
      }
    }

    const browse = async () => {
      if (browsing.value || ws.switching.value) return
      browsing.value = true
      try {
        const result = await wailsCall('SelectWorkspaceDirectory')
        if (!result.success) throw new Error(result.error || '无法打开文件夹选择器')
        if (!result.cancelled && result.path) await pick(result.path)
      } catch (e:any) { notify.error(e.message || '选择文件夹失败') }
      finally { browsing.value = false }
    }

    return () => {
      const cur = ws.info.value
      return (
        <div class="ws-switch">
          <button
            class="ws-switch-btn"
            title={cur ? `当前工作区：${cur.root}` : '工作区信息不可用'}
            aria-haspopup="menu"
            aria-expanded={open.value}
            onClick={toggle}
          >
            <FolderOpen size={13} />
            <span class="ws-switch-name">{cur?.name || '未选择工作区'}</span>
            {cur?.branch && <span class="ws-switch-branch">{cur.branch}</span>}
            <ChevronDown size={11} />
          </button>
          {open.value && (
            <>
              <div class="ws-switch-backdrop" onClick={() => { open.value = false }} />
              <div class="ws-switch-menu" role="menu">
                <button type="button" class="ws-browse-folder" disabled={ws.switching.value || browsing.value} onClick={browse}><FolderOpen size={15}/><span>{browsing.value ? '正在选择…' : '浏览文件夹…'}</span></button>
                <div class="ws-switch-head">最近的工作区</div>
                {ws.recent.value.length === 0 && <div class="ws-switch-empty">暂无记录</div>}
                {ws.recent.value.map(p => (
                  <button
                    key={p}
                    class={['ws-switch-item', cur?.root?.toLowerCase() === p.toLowerCase() && 'active']}
                    title={p}
                    role="menuitem"
                    disabled={ws.switching.value || browsing.value}
                    onClick={() => pick(p)}
                  >
                    <span class="ws-switch-item-name">{ws.nameOf(p)}</span>
                    <span class="ws-switch-item-path">{p}</span>
                    {cur?.root?.toLowerCase() === p.toLowerCase() && <Check size={12} />}
                  </button>
                ))}
                <div class="ws-switch-add">
                  <input
                    ref={input}
                    class="ws-switch-input"
                    placeholder="输入目录路径，回车切换"
                    aria-label="工作区路径"
                    value={draft.value}
                    disabled={ws.switching.value}
                    onInput={(e: Event) => { draft.value = (e.target as HTMLInputElement).value }}
                    onKeydown={(e: KeyboardEvent) => {
                      if (e.key === 'Enter') void pick(draft.value)
                      if (e.key === 'Escape') open.value = false
                    }}
                  />
                </div>
              </div>
            </>
          )}
        </div>
      )
    }
  },
})
