import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '../api/client'
import { RelationPicker } from './RelationPicker'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('关联内容选择器', () => {
  it('缺少目标模型权限时保留现有 ID 且不请求接口', () => {
    const listEntries = vi.spyOn(api, 'listEntries')
    render(<RelationPicker multiple={false} value="ent_existing" onChange={vi.fn()} disabled={false} targetModelId="mdl_target" canViewTarget={false} />)
    expect(screen.getByText('无目标模型内容查看权限')).toBeVisible()
    expect(screen.getByTitle('ent_existing')).toBeVisible()
    expect(listEntries).not.toHaveBeenCalled()
  })
})
