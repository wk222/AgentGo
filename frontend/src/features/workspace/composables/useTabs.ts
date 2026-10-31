import { ref, computed } from 'vue'
import * as monaco from 'monaco-editor'
import { wailsCall } from '../../../shared/wails'
import { getMonacoLanguage } from '../../../shared/monaco'
import type { OpenTab, WorkspaceFileItem } from '../types'
import { notify, confirmDialog } from '../../../shared/ui/notify'

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
      const res = await wailsCall('ReadWorkspaceFile', item.path)
      if (res?.error) {
        notify.error(res.error)
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
      notify.error(`无法打开文件: ${e?.message || e}`)
      return null
    }
  }

  const closeTab = async (path: string) => {
    const first = openTabs.value.find((t) => t.path === path)
    if (!first) return

    if (first.isDirty) {
      const ok = await confirmDialog(`文件 "${first.name}" 尚未保存，确定要关闭吗？`, {
        title: '关闭未保存的文件', okText: '不保存并关闭', danger: true,
      })
      if (!ok) return
    }

    // 对话框期间标签可能已变化，关闭前重新定位。
    const idx = openTabs.value.findIndex((t) => t.path === path)
    if (idx === -1) return
    const tab = openTabs.value[idx]

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
      const res = await wailsCall('WriteWorkspaceFile', tab.path, currentContent)
      if (res?.error) {
        notify.error(res.error)
        return false
      }
      tab.content = currentContent
      tab.originalContent = currentContent
      tab.isDirty = false
      return true
    } catch (e: any) {
      notify.error(`保存失败: ${e?.message || e}`)
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
      const res = await wailsCall('ReadWorkspaceFile', path)
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
