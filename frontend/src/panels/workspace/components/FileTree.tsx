import { defineComponent, ref, reactive, computed, type PropType } from 'vue'
import type { WorkspaceFileItem } from '../types'
import { FileIcon, GitStatusBadge } from '../utils/icons'

export const FileTree = defineComponent({
  name: 'FileTree',
  props: {
    rootFiles: { type: Array as PropType<WorkspaceFileItem[]>, required: true },
    expandedPaths: { type: Object as PropType<Set<string>>, required: true },
    childrenMap: { type: Object as PropType<Record<string, WorkspaceFileItem[]>>, required: true },
    workspaceRoot: { type: String, default: '' },
    activePath: { type: String, default: '' },
    inlineCreating: {
      type: Object as PropType<{ active: boolean; parentPath: string; type: 'file' | 'dir'; value: string }>,
      required: true,
    },
    inlineRenaming: {
      type: Object as PropType<{ active: boolean; targetPath: string; oldName: string; value: string }>,
      required: true,
    },
  },
  emits: [
    'selectFile',
    'toggleDir',
    'startCreate',
    'submitCreate',
    'cancelCreate',
    'startRename',
    'submitRename',
    'cancelRename',
    'confirmDelete',
    'refresh',
    'changeRoot',
  ],
  setup(props, { emit }) {
    const searchQuery = ref('')

    // Context Menu State
    const contextMenu = reactive<{
      visible: boolean
      x: number
      y: number
      item: WorkspaceFileItem | null
    }>({
      visible: false,
      x: 0,
      y: 0,
      item: null,
    })

    const onContextMenu = (e: MouseEvent, item: WorkspaceFileItem) => {
      e.preventDefault()
      e.stopPropagation()
      contextMenu.visible = true
      contextMenu.x = e.clientX
      contextMenu.y = e.clientY
      contextMenu.item = item
    }

    const closeContextMenu = () => {
      contextMenu.visible = false
      contextMenu.item = null
    }

    const copyText = (text: string) => {
      navigator.clipboard?.writeText(text)
      closeContextMenu()
    }

    const renderTreeItem = (item: WorkspaceFileItem, depth: number = 0) => {
      const isExpanded = props.expandedPaths.has(item.path)
      const isActive = item.path === props.activePath
      const isRenaming = props.inlineRenaming.active && props.inlineRenaming.targetPath === item.path

      // Filter check
      if (searchQuery.value && !item.is_dir && !item.name.toLowerCase().includes(searchQuery.value.toLowerCase())) {
        return null
      }

      return (
        <div key={item.path}>
          <div
            onClick={() => {
              if (item.is_dir) {
                emit('toggleDir', item)
              } else {
                emit('selectFile', item)
              }
            }}
            onContextmenu={(e) => onContextMenu(e, item)}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '6px',
              paddingLeft: `${depth * 14 + 10}px`,
              paddingRight: '8px',
              height: '24px',
              fontSize: '12px',
              fontFamily: 'var(--font-ui, sans-serif)',
              color: isActive ? 'var(--text-strong, #FFFFFF)' : 'var(--text, #E4E4E7)',
              backgroundColor: isActive ? 'rgba(56, 189, 248, 0.12)' : 'transparent',
              borderLeft: isActive ? '2px solid var(--accent, #38BDF8)' : '2px solid transparent',
              cursor: 'pointer',
              userSelect: 'none',
              transition: 'background-color 0.1s ease',
            }}
            onMouseenter={(e) => {
              if (!isActive) (e.currentTarget as HTMLElement).style.backgroundColor = 'rgba(255,255,255,0.04)'
            }}
            onMouseleave={(e) => {
              if (!isActive) (e.currentTarget as HTMLElement).style.backgroundColor = 'transparent'
            }}
          >
            {/* Expand / Collapse Chevron */}
            {item.is_dir ? (
              <span
                style={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  width: '12px',
                  height: '12px',
                  transform: isExpanded ? 'rotate(90deg)' : 'rotate(0deg)',
                  transition: 'transform 0.15s ease',
                  opacity: 0.7,
                }}
              >
                <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                  <polyline points="9 18 15 12 9 6" />
                </svg>
              </span>
            ) : (
              <span style={{ width: '12px' }} />
            )}

            {/* Icon */}
            <FileIcon fileName={item.name} isDir={item.is_dir} isOpen={isExpanded} size={14} />

            {/* Item Name / Inline Rename Input */}
            {isRenaming ? (
              <input
                type="text"
                value={props.inlineRenaming.value}
                onInput={(e) => { props.inlineRenaming.value = (e.target as HTMLInputElement).value }}
                onKeydown={(e) => {
                  if (e.key === 'Enter') emit('submitRename')
                  if (e.key === 'Escape') emit('cancelRename')
                }}
                onClick={(e) => e.stopPropagation()}
                autofocus
                style={{
                  flex: 1,
                  height: '20px',
                  fontSize: '12px',
                  padding: '0 4px',
                  backgroundColor: '#27272A',
                  color: '#FFFFFF',
                  border: '1px solid var(--accent, #38BDF8)',
                  borderRadius: '3px',
                  outline: 'none',
                }}
              />
            ) : (
              <span
                style={{
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                  flex: 1,
                  fontWeight: item.is_dir ? 500 : 400,
                }}
              >
                {item.name}
              </span>
            )}

            {/* Git Status Badge */}
            <GitStatusBadge status={item.git_status} />
          </div>

          {/* Inline creation under this directory */}
          {item.is_dir && isExpanded && props.inlineCreating.active && props.inlineCreating.parentPath === item.path && (
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '6px',
                paddingLeft: `${(depth + 1) * 14 + 10}px`,
                paddingRight: '8px',
                height: '24px',
              }}
            >
              <span style={{ width: '12px' }} />
              <FileIcon fileName={props.inlineCreating.type === 'dir' ? '' : 'new.ts'} isDir={props.inlineCreating.type === 'dir'} size={14} />
              <input
                type="text"
                placeholder={props.inlineCreating.type === 'dir' ? '文件夹名称...' : '文件名称...'}
                value={props.inlineCreating.value}
                onInput={(e) => { props.inlineCreating.value = (e.target as HTMLInputElement).value }}
                onKeydown={(e) => {
                  if (e.key === 'Enter') emit('submitCreate')
                  if (e.key === 'Escape') emit('cancelCreate')
                }}
                autofocus
                style={{
                  flex: 1,
                  height: '20px',
                  fontSize: '12px',
                  padding: '0 4px',
                  backgroundColor: '#27272A',
                  color: '#FFFFFF',
                  border: '1px solid var(--accent, #38BDF8)',
                  borderRadius: '3px',
                  outline: 'none',
                }}
              />
            </div>
          )}

          {/* Children items */}
          {item.is_dir && isExpanded && props.childrenMap[item.path] && (
            <div>
              {props.childrenMap[item.path].map((child) => renderTreeItem(child, depth + 1))}
            </div>
          )}
        </div>
      )
    }

    return () => (
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          height: '100%',
          overflow: 'hidden',
          backgroundColor: 'var(--sidebar, #18181B)',
        }}
        onClick={closeContextMenu}
      >
        {/* Header Toolbar */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '6px 10px',
            borderBottom: '1px solid var(--border-subtle, rgba(255,255,255,0.06))',
          }}
        >
          <span
            title={props.workspaceRoot || 'Workspace'}
            style={{ fontSize: '11px', fontWeight: 700, color: 'var(--text-dim, #A1A1AA)', textTransform: 'uppercase', letterSpacing: '0.05em', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
          >
            Explorer
          </span>

          <div style={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
            <button
              onClick={() => emit('changeRoot')}
              title="切换工作区"
              style={{ background: 'transparent', border: 'none', color: 'var(--text-dim, #A1A1AA)', cursor: 'pointer', padding: '3px', borderRadius: '3px' }}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M3 7h6l2 2h10v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
                <path d="M3 7V5a2 2 0 0 1 2-2h4l2 2h4" />
              </svg>
            </button>

            <button
              onClick={() => emit('startCreate', '', 'file')}
              title="新建文件"
              style={{ background: 'transparent', border: 'none', color: 'var(--text-dim, #A1A1AA)', cursor: 'pointer', padding: '3px', borderRadius: '3px' }}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                <polyline points="14 2 14 8 20 8" />
                <line x1="12" y1="18" x2="12" y2="12" />
                <line x1="9" y1="15" x2="15" y2="15" />
              </svg>
            </button>

            <button
              onClick={() => emit('startCreate', '', 'dir')}
              title="新建文件夹"
              style={{ background: 'transparent', border: 'none', color: 'var(--text-dim, #A1A1AA)', cursor: 'pointer', padding: '3px', borderRadius: '3px' }}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                <line x1="12" y1="11" x2="12" y2="17" />
                <line x1="9" y1="14" x2="15" y2="14" />
              </svg>
            </button>

            <button
              onClick={() => emit('refresh')}
              title="刷新工作区"
              style={{ background: 'transparent', border: 'none', color: 'var(--text-dim, #A1A1AA)', cursor: 'pointer', padding: '3px', borderRadius: '3px' }}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M23 4v6h-6" />
                <path d="M1 20v-6h6" />
                <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
              </svg>
            </button>
          </div>
        </div>

        {/* Quick Filter Search */}
        <div style={{ padding: '6px 8px', borderBottom: '1px solid var(--border-subtle, rgba(255,255,255,0.06))' }}>
          <input
            type="text"
            placeholder="过滤文件..."
            value={searchQuery.value}
            onInput={(e) => { searchQuery.value = (e.target as HTMLInputElement).value }}
            style={{
              width: '100%',
              height: '24px',
              backgroundColor: 'rgba(0,0,0,0.25)',
              border: '1px solid var(--border-subtle, rgba(255,255,255,0.1))',
              borderRadius: '4px',
              color: 'var(--text, #E4E4E7)',
              fontSize: '11px',
              padding: '0 8px',
              outline: 'none',
            }}
          />
        </div>

        {/* Root Inline Create */}
        {props.inlineCreating.active && props.inlineCreating.parentPath === '' && (
          <div style={{ display: 'flex', alignItems: 'center', gap: '6px', padding: '4px 10px', height: '24px' }}>
            <FileIcon fileName={props.inlineCreating.type === 'dir' ? '' : 'new.ts'} isDir={props.inlineCreating.type === 'dir'} size={14} />
            <input
              type="text"
              placeholder={props.inlineCreating.type === 'dir' ? '文件夹名称...' : '文件名称...'}
              value={props.inlineCreating.value}
              onInput={(e) => { props.inlineCreating.value = (e.target as HTMLInputElement).value }}
              onKeydown={(e) => {
                if (e.key === 'Enter') emit('submitCreate')
                if (e.key === 'Escape') emit('cancelCreate')
              }}
              autofocus
              style={{
                flex: 1,
                height: '20px',
                fontSize: '12px',
                padding: '0 4px',
                backgroundColor: '#27272A',
                color: '#FFFFFF',
                border: '1px solid var(--accent, #38BDF8)',
                borderRadius: '3px',
                outline: 'none',
              }}
            />
          </div>
        )}

        {/* File Tree List */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '4px 0' }}>
          {props.rootFiles.length === 0 ? (
            <div style={{ padding: '20px 10px', textAlign: 'center', color: 'var(--text-dim, #71717A)', fontSize: '11px' }}>
              暂无文件
            </div>
          ) : (
            props.rootFiles.map((item) => renderTreeItem(item, 0))
          )}
        </div>

        {/* Context Menu */}
        {contextMenu.visible && contextMenu.item && (
          <div
            style={{
              position: 'fixed',
              top: `${contextMenu.y}px`,
              left: `${contextMenu.x}px`,
              backgroundColor: 'var(--surface, #27272A)',
              border: '1px solid var(--border, rgba(255,255,255,0.15))',
              borderRadius: '6px',
              padding: '4px 0',
              zIndex: 9999,
              boxShadow: '0 8px 24px rgba(0,0,0,0.5)',
              minWidth: '160px',
              fontSize: '12px',
            }}
          >
            {contextMenu.item.is_dir && (
              <>
                <div
                  style={{ padding: '6px 12px', cursor: 'pointer', color: 'var(--text, #E4E4E7)' }}
                  onClick={() => {
                    emit('startCreate', contextMenu.item!.path, 'file')
                    closeContextMenu()
                  }}
                >
                  新建文件...
                </div>
                <div
                  style={{ padding: '6px 12px', cursor: 'pointer', color: 'var(--text, #E4E4E7)' }}
                  onClick={() => {
                    emit('startCreate', contextMenu.item!.path, 'dir')
                    closeContextMenu()
                  }}
                >
                  新建文件夹...
                </div>
                <div style={{ height: '1px', backgroundColor: 'rgba(255,255,255,0.08)', margin: '4px 0' }} />
              </>
            )}

            <div
              style={{ padding: '6px 12px', cursor: 'pointer', color: 'var(--text, #E4E4E7)' }}
              onClick={() => {
                emit('startRename', contextMenu.item!)
                closeContextMenu()
              }}
            >
              重命名
            </div>

            <div
              style={{ padding: '6px 12px', cursor: 'pointer', color: '#EF4444' }}
              onClick={() => {
                emit('confirmDelete', contextMenu.item!)
                closeContextMenu()
              }}
            >
              删除
            </div>

            <div style={{ height: '1px', backgroundColor: 'rgba(255,255,255,0.08)', margin: '4px 0' }} />

            <div
              style={{ padding: '6px 12px', cursor: 'pointer', color: 'var(--text, #E4E4E7)' }}
              onClick={() => copyText(contextMenu.item!.path)}
            >
              复制相对路径
            </div>
          </div>
        )}
      </div>
    )
  },
})
