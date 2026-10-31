/// <reference types="vite/client" />
import type { Component } from 'vue'

// Optional, locally injected settings extensions. Files named *.private.tsx are
// never committed; a public checkout simply has none and the UI hides the slot.
interface PrivateSettingsModule {
  default?: Component
  providerLabel?: (apiBase: string, model: string) => string | undefined
}

const loaded = Object.values(
  import.meta.glob<PrivateSettingsModule>('./*.private.tsx', { eager: true }),
)

export const PrivateAccountCard = (loaded.find(m => m.default)?.default ?? null) as (new () => { $props: { onConnected: () => void } }) | null

export function privateProviderLabel(apiBase: string, model: string): string | undefined {
  for (const m of loaded) {
    const label = m.providerLabel?.(apiBase, model)
    if (label) return label
  }
  return undefined
}
