import { ref, reactive, computed } from 'vue'
import { wailsCall } from '../../../wails'
import type { WorkspaceFileItem } from '../types'

export function useWorkspaceState(initialRoot: string = '') {
  const workspaceRoot = ref(initialRoot)
  const rootFiles = ref<WorkspaceFileItem[]>([])
  const expandedPaths = ref<Set<string>>(new Set())
  const childrenMap = reactive<Record<string, WorkspaceFileItem[]>>({})
  const loading = ref(false)
  const errorMsg = ref('')
  const gitBranch = ref('main')

  // Inline creation state
  const inlineCreating = reactive<{
    active: boolean
    parentPath: string
    type: 'file' | 'dir'
    value: string
  }>({
    active: false,
    parentPath: '',
    type: 'file',
    value: '',
  })

  // Inline renaming state
  const inlineRenaming = reactive<{
    active: boolean
    targetPath: string
    oldName: string
    value: string
  }>({
    active: false,
    targetPath: '',
    oldName: '',
    value: '',
  })

  // Delete modal state
  const deleteModal = reactive<{
    visible: boolean
    item: WorkspaceFileItem | null
  }>({
    visible: false,
    item: null,
  })

  const loadWorkspaceRoot = async (targetPath?: string) => {
    loading.value = true
    errorMsg.value = ''
    try {
      if (targetPath) {
        const res = await wailsCall<{ root?: string; error?: string }>('SetWorkspaceRoot', targetPath)
        if (res?.error) {
          errorMsg.value = res.error
          return
        }
        if (res?.root) {
          workspaceRoot.value = res.root
        }
      } else if (!workspaceRoot.value) {
        const info = await wailsCall<{ root?: string }>('GetWorkspaceInfo')
        if (info?.root) {
          workspaceRoot.value = info.root
        }
      }

      const files = await wailsCall<WorkspaceFileItem[]>('ListWorkspace', '')
      rootFiles.value = Array.isArray(files) ? files : []

      // Refresh git branch
      try {
        const branchRes = await wailsCall<{ stdout?: string }>('ExecuteTerminalCommand', 'git branch --show-current')
        if (branchRes?.stdout?.trim()) {
          gitBranch.value = branchRes.stdout.trim()
        }
      } catch {
        // Not a git repo or git not found
      }
    } catch (e: any) {
      errorMsg.value = e?.message || '加载工作区失败'
    } finally {
      loading.value = false
    }
  }

  const toggleDirectory = async (item: WorkspaceFileItem) => {
    if (!item.is_dir) return
    const isExpanded = expandedPaths.value.has(item.path)
    if (isExpanded) {
      expandedPaths.value.delete(item.path)
    } else {
      expandedPaths.value.add(item.path)
      if (!childrenMap[item.path]) {
        try {
          const files = await wailsCall<WorkspaceFileItem[]>('ListWorkspaceDir', item.path)
          childrenMap[item.path] = Array.isArray(files) ? files : []
        } catch (e: any) {
          console.error(`Failed to list dir: ${item.path}`, e)
        }
      }
    }
  }

  const refreshDirectory = async (parentPath: string) => {
    try {
      if (!parentPath) {
        const files = await wailsCall<WorkspaceFileItem[]>('ListWorkspace', '')
        rootFiles.value = Array.isArray(files) ? files : []
      } else {
        const files = await wailsCall<WorkspaceFileItem[]>('ListWorkspaceDir', parentPath)
        childrenMap[parentPath] = Array.isArray(files) ? files : []
      }
    } catch (e) {
      console.error(`Failed to refresh dir: ${parentPath}`, e)
    }
  }

  const startInlineCreate = (parentPath: string, type: 'file' | 'dir') => {
    if (parentPath && !expandedPaths.value.has(parentPath)) {
      expandedPaths.value.add(parentPath)
    }
    inlineCreating.parentPath = parentPath
    inlineCreating.type = type
    inlineCreating.value = ''
    inlineCreating.active = true
  }

  const submitInlineCreate = async () => {
    if (!inlineCreating.active) return
    const name = inlineCreating.value.trim()
    if (!name) {
      inlineCreating.active = false
      return
    }

    const relPath = inlineCreating.parentPath ? `${inlineCreating.parentPath}/${name}` : name
    const method = inlineCreating.type === 'dir' ? 'CreateWorkspaceDir' : 'CreateWorkspaceFile'

    try {
      const res = await wailsCall<{ error?: string }>(method, relPath)
      if (res?.error) {
        alert(res.error)
      } else {
        await refreshDirectory(inlineCreating.parentPath)
      }
    } catch (e: any) {
      alert(`创建失败: ${e?.message || e}`)
    } finally {
      inlineCreating.active = false
    }
  }

  const cancelInlineCreate = () => {
    inlineCreating.active = false
    inlineCreating.value = ''
  }

  const startInlineRename = (item: WorkspaceFileItem) => {
    inlineRenaming.targetPath = item.path
    inlineRenaming.oldName = item.name
    inlineRenaming.value = item.name
    inlineRenaming.active = true
  }

  const submitInlineRename = async () => {
    if (!inlineRenaming.active) return
    const newName = inlineRenaming.value.trim()
    if (!newName || newName === inlineRenaming.oldName) {
      inlineRenaming.active = false
      return
    }

    const parentPath = inlineRenaming.targetPath.includes('/')
      ? inlineRenaming.targetPath.substring(0, inlineRenaming.targetPath.lastIndexOf('/'))
      : ''
    const newPath = parentPath ? `${parentPath}/${newName}` : newName

    try {
      const res = await wailsCall<{ error?: string }>('RenameWorkspacePath', inlineRenaming.targetPath, newPath)
      if (res?.error) {
        alert(res.error)
      } else {
        await refreshDirectory(parentPath)
      }
    } catch (e: any) {
      alert(`重命名失败: ${e?.message || e}`)
    } finally {
      inlineRenaming.active = false
    }
  }

  const cancelInlineRename = () => {
    inlineRenaming.active = false
    inlineRenaming.value = ''
  }

  const confirmDelete = (item: WorkspaceFileItem) => {
    deleteModal.item = item
    deleteModal.visible = true
  }

  const executeDelete = async () => {
    if (!deleteModal.item) return
    const item = deleteModal.item
    const parentPath = item.path.includes('/')
      ? item.path.substring(0, item.path.lastIndexOf('/'))
      : ''

    try {
      const res = await wailsCall<{ error?: string }>('DeleteWorkspacePath', item.path)
      if (res?.error) {
        alert(res.error)
      } else {
        await refreshDirectory(parentPath)
      }
    } catch (e: any) {
      alert(`删除失败: ${e?.message || e}`)
    } finally {
      deleteModal.visible = false
      deleteModal.item = null
    }
  }

  const cancelDelete = () => {
    deleteModal.visible = false
    deleteModal.item = null
  }

  return {
    workspaceRoot,
    rootFiles,
    expandedPaths,
    childrenMap,
    loading,
    errorMsg,
    gitBranch,
    inlineCreating,
    inlineRenaming,
    deleteModal,
    loadWorkspaceRoot,
    toggleDirectory,
    refreshDirectory,
    startInlineCreate,
    submitInlineCreate,
    cancelInlineCreate,
    startInlineRename,
    submitInlineRename,
    cancelInlineRename,
    confirmDelete,
    executeDelete,
    cancelDelete,
  }
}
