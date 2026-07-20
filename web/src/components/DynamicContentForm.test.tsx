import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { ContentField } from '../api/types'
import { DynamicContentForm } from './DynamicContentForm'

afterEach(cleanup)

function field(type: ContentField['type'], overrides: Partial<ContentField> = {}): ContentField {
  return { id: type, key: type, display_name: type, description: '', type, required: false, default_value: null, constraints: {}, children: [], status: 'active', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', ...overrides }
}

describe('动态内容表单', () => {
  it('按模型字段更新文本和结构化富文本值', () => {
    const onChange = vi.fn()
    const { rerender } = render(<DynamicContentForm fields={[field('single_line_text')]} content={{}} onChange={onChange} />)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '标题' } })
    expect(onChange).toHaveBeenCalledWith({ single_line_text: '标题' })

    rerender(<DynamicContentForm fields={[field('rich_text')]} content={{ rich_text: { type: 'doc' } }} onChange={onChange} />)
    expect(screen.getByLabelText('rich_text JSON')).toHaveValue('{\n  "type": "doc"\n}')
  })

  it('单媒体提供素材选择器并保留现有素材 ID', () => {
    render(<DynamicContentForm fields={[field('single_media')]} content={{ single_media: 'ast_cover' }} onChange={vi.fn()} />)
    expect(screen.getByRole('button', { name: '更换素材' })).toBeVisible()
    expect(screen.getByText('ast_cover')).toBeVisible()
  })

  it('多媒体按首次出现顺序去重并限制为 50 项', () => {
    const ids = Array.from({ length: 52 }, (_, index) => `ast_${index}`)
    render(<DynamicContentForm fields={[field('multi_media')]} content={{ multi_media: ['ast_0', 'ast_0', ...ids.slice(1)] }} onChange={vi.fn()} />)
    expect(screen.getByText('已选 50 / 50')).toBeVisible()
    expect(screen.getByText('1.')).toBeVisible()
    expect(screen.queryByText('ast_50')).not.toBeInTheDocument()
  })

  it('允许编辑单关联条目 ID', () => {
    const onChange = vi.fn()
    render(<DynamicContentForm fields={[field('single_relation')]} content={{}} onChange={onChange} />)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'ent_target' } })
    expect(onChange).toHaveBeenCalledWith({ single_relation: 'ent_target' })
  })
})
