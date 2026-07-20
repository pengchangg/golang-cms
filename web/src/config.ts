export function isAssetsEnabled(value = import.meta.env.VITE_ASSETS_ENABLED) {
  return value !== 'false'
}

export const ASSETS_ENABLED = isAssetsEnabled()
