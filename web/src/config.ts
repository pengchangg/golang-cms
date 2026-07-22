export function isAssetsEnabled(value = import.meta.env.VITE_ASSETS_ENABLED) {
  return value !== 'false'
}

export function isContentAPIExplorerEnabled(value = import.meta.env.VITE_CONTENT_API_EXPLORER_ENABLED, dev = import.meta.env.DEV) {
  return value === 'true' || (value === undefined && dev)
}

export const ASSETS_ENABLED = isAssetsEnabled()
export const CONTENT_API_EXPLORER_ENABLED = isContentAPIExplorerEnabled()
