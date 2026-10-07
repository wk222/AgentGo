import { ref, computed } from 'vue'
import * as monaco from 'monaco-editor'
import { wailsCall } from '../../../wails'
import { getMonacoLanguage } from '../../../utils/monaco'
import type { OpenTab, WorkspaceFileItem } from '../types'

export function useTabs() {
  const openTabs = ref<OpenTab[]>([])
  const activeTabPath = ref<string>('')

  const activeTab = computed(() => {
    return openTabs.value.find((t) => t.path === activeTabPath.value) || null
  })

  const openFile = async (item: WorkspaceFileItem | { path: string; name: string }) => {
    const existing = openTabs.value.find((t) => t.path === item.path)
    if (existing) {
      activeTabPath.value = item.path
      return existing
    }

    try {
      const res = await wailsCall<{ content?: string; error?: string }>('ReadWorkspaceFile', item.path)
      if (res?.error) {
        alert(res.error)
        return null
      }

      const content = res?.content ?? ''
      const lang = getMonacoLanguage(item.name)
      const eol: 'LF' | 'CRLF' = content.includes('\r\n') ? 'CRLF' : 'LF'

      const newTab: OpenTab = {
        id: `tab_${Date.now()}_${Math.random().toString(36).substring(2, 7)}`,
        path: item.path,
        name: item.name,
        content,
        originalContent: content,
        isDirty: false,
        diffMode: false,
        diffContent: '',
        language: lang,
        encoding: 'UTF-8',
        eol,
        indentSize: 2,
        useSpaces: true,
      }

      openTabs.value.push(newTab)
      activeTabPath.value = item.path
      return newTab
    } catch (e: any) {
      alert(`无法打开文件: ${e?.message || e}`)
      return null
    }
  }

  const closeTab = (path: string) => {
    const idx = openTabs.value.findIndex((t) => t.path === path)
    if (idx === -1) return

    const tab = openTabs.value[idx]
    if (tab.isDirty) {
      const ok = confirm(`文件 "${tab.name}" 尚未保存，确定要关闭吗？`)
      if (!ok) return
    }

    if (tab.model) {
      tab.model.dispose()
      tab.model = undefined
    }

    openTabs.value.splice(idx, 1)

    if (activeTabPath.value === path) {
      if (openTabs.value.length > 0) {
        const nextIdx = Math.min(idx, openTabs.value.length - 1)
        activeTabPath.value = openTabs.value[nextIdx].path
      } else {
        activeTabPath.value = ''
      }
    }
  }

  const closeOtherTabs = (path: string) => {
    const keep = openTabs.value.find((t) => t.path === path)
    if (!keep) return

    for (const t of openTabs.value) {
      if (t.path !== path && t.model) {
        t.model.dispose()
        t.model = undefined
      }
    }

    openTabs.value = [keep]
    activeTabPath.value = path
  }

  const closeAllTabs = () => {
    for (const t of openTabs.value) {
      if (t.model) {
        t.model.dispose()
        t.model = undefined
      }
    }
    openTabs.value = []
    activeTabPath.value = ''
  }

  const saveTab = async (tab: OpenTab) => {
    if (!tab) return false
    const currentContent = tab.model ? tab.model.getValue() : tab.content
    try {
      const res = await wailsCall<{ error?: string }>('WriteWorkspaceFile', tab.path, currentContent)
      if (res?.error) {
        alert(res.error)
        return false
      }
      tab.content = currentContent
      tab.originalContent = currentContent
      tab.isDirty = false
      return true
    } catch (e: any) {
      alert(`保存失败: ${e?.message || e}`)
      return false
    }
  }

  const saveActiveTab = async () => {
    if (activeTab.value) {
      return await saveTab(activeTab.value)
    }
    return false
  }

  const saveAllTabs = async () => {
    for (const tab of openTabs.value) {
      if (tab.isDirty) {
        await saveTab(tab)
      }
    }
  }

  const reloadPath = async (path: string) => {
    const tab = openTabs.value.find((item) => item.path === path)
    if (!tab) return
    try {
      const res = await wailsCall<{ success?: boolean; content?: string; error?: string }>('ReadWorkspaceFile', path)
      if (res?.success === false || res?.error) {
        if (tab.model) tab.model.dispose()
        openTabs.value = openTabs.value.filter((item) => item.path !== path)
        if (activeTabPath.value === path) activeTabPath.value = openTabs.value[0]?.path || ''
        return
      }
      const content = res?.content ?? ''
      tab.content = content
      tab.originalContent = content
      tab.isDirty = false
      tab.model?.setValue(content)
    } catch {
      // A review restore can remove an Agent-created file; close stale tabs.
      if (tab.model) tab.model.dispose()
      openTabs.value = openTabs.value.filter((item) => item.path !== path)
      if (activeTabPath.value === path) activeTabPath.value = openTabs.value[0]?.path || ''
    }
  }

  return {
    openTabs,
    activeTabPath,
    activeTab,
    openFile,
    closeTab,
    closeOtherTabs,
    closeAllTabs,
    saveTab,
    saveActiveTab,
    saveAllTabs,
    reloadPath,
  }
}
