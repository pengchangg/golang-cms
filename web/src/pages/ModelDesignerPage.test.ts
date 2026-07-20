import { describe, expect, it } from 'vitest'

import type { ContentModelSummary } from '../api/types'
import { relationModelOptions } from '../relationModels'

const now = '2026-07-20T08:00:00Z'

function model(id: string, key: string, displayName: string): ContentModelSummary {
  return { id, key, display_name: displayName, description: '', status: 'active', created_at: now, updated_at: now }
}

describe('关联目标模型选项', () => {
  it('排除当前模型并用模型 ID 作为提交值', () => {
    const options = relationModelOptions([model('mdl_current', 'articles', '文章'), model('mdl_author', 'authors', '作者')], 'mdl_current')
    expect(options).toEqual([{ value: 'mdl_author', label: '作者', searchText: '作者 authors', key: 'authors' }])
  })
})
