import { describe, expect, it } from 'vitest'

import { isAssetsEnabled, isContentAPIExplorerEnabled } from './config'

describe('素材能力配置', () => {
  it('默认启用且仅显式 false 禁用', () => {
    expect(isAssetsEnabled(undefined)).toBe(true)
    expect(isAssetsEnabled('true')).toBe(true)
    expect(isAssetsEnabled('false')).toBe(false)
  })
})

describe('客户端调试配置', () => {
  it('仅在显式开启或 Vite 开发服务器中启用', () => {
    expect(isContentAPIExplorerEnabled(undefined, true)).toBe(true)
    expect(isContentAPIExplorerEnabled(undefined, false)).toBe(false)
    expect(isContentAPIExplorerEnabled('true', false)).toBe(true)
    expect(isContentAPIExplorerEnabled('false', true)).toBe(false)
  })
})
