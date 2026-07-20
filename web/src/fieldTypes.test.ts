import { describe, expect, it } from 'vitest'

import { fieldTypeMeta, fieldTypeOptions } from './fieldTypes'

describe('字段类型中文元数据', () => {
  it('完整覆盖全部字段类型并提供中文说明', () => {
    expect(fieldTypeOptions).toHaveLength(17)
    expect(Object.keys(fieldTypeMeta)).toHaveLength(17)
    for (const option of fieldTypeOptions) {
      expect(option.label).toMatch(/[\u4e00-\u9fff]/)
      expect(option.description).toMatch(/[\u4e00-\u9fff]/)
      expect(option.label).not.toBe(option.value)
    }
  })
})
