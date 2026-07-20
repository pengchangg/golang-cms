import { describe, expect, it } from 'vitest'

import { isAssetsEnabled } from './config'

describe('素材能力配置', () => {
  it('默认启用且仅显式 false 禁用', () => {
    expect(isAssetsEnabled(undefined)).toBe(true)
    expect(isAssetsEnabled('true')).toBe(true)
    expect(isAssetsEnabled('false')).toBe(false)
  })
})
