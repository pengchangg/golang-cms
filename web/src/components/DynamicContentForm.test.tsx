import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '../api/client'
import type { ContentField } from '../api/types'
import { DynamicContentForm } from './DynamicContentForm'

vi.mock('./RichTextEditor', () => ({
  RichTextEditor: ({ label, disabled }: { label: string; disabled?: boolean }) => (
    <div className={`rich-text-editor${disabled ? ' is-disabled' : ''}`} role="textbox" aria-label={label} contentEditable={!disabled} />
  ),
}))

afterEach(cleanup)

function field(type: ContentField['type'], overrides: Partial<ContentField> = {}): ContentField {
  return { id: type, key: type, display_name: type, description: '', type, required: false, default_value: null, constraints: {}, children: [], status: 'active', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', ...overrides }
}

async function openComplexEditor(label = '展开编辑') {
  await userEvent.click(screen.getByRole('button', { name: label }))
  return within(await screen.findByRole('dialog'))
}

describe('动态内容表单', () => {
  it('按模型字段更新文本和结构化富文本值', async () => {
    const onChange = vi.fn()
    const { rerender } = render(<DynamicContentForm fields={[field('single_line_text')]} content={{}} onChange={onChange} />)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '标题' } })
    expect(onChange).toHaveBeenCalledWith({ single_line_text: '标题' })
    expect(screen.getByText(/单行文本/)).toBeVisible()

    rerender(<DynamicContentForm fields={[field('rich_text')]} content={{ rich_text: '<p>摘要正文</p>' }} onChange={onChange} />)
    expect(screen.getByText('摘要正文')).toBeVisible()
    expect(screen.getByRole('button', { name: '展开编辑' })).toBeVisible()
    await openComplexEditor()
    expect(await screen.findByRole('textbox', { name: 'rich_text' })).toBeVisible()
  })

  it('单媒体提供素材选择器并保留现有素材 ID', () => {
    render(<DynamicContentForm fields={[field('single_media')]} content={{ single_media: 'ast_cover' }} onChange={vi.fn()} />)
    expect(screen.getByRole('button', { name: 'single_media 更换素材' })).toBeVisible()
    expect(screen.getByText('ast_cover')).toBeVisible()
  })

  it('多媒体按首次出现顺序去重并限制为 50 项', () => {
    const ids = Array.from({ length: 52 }, (_, index) => `ast_${index}`)
    render(<DynamicContentForm fields={[field('multi_media')]} content={{ multi_media: ['ast_0', 'ast_0', ...ids.slice(1)] }} onChange={vi.fn()} />)
    expect(screen.getByText('已选 50 / 50')).toBeVisible()
    expect(screen.getByText('ast_0')).toBeVisible()
    expect(screen.getByRole('button', { name: '移除素材 1' })).toBeVisible()
    expect(screen.queryByText('ast_50')).not.toBeInTheDocument()
  })

  it('从目标模型内容中选择单关联条目', async () => {
    const onChange = vi.fn()
    vi.spyOn(api, 'listEntries').mockResolvedValue({ items: [{ id: 'ent_target', model_id: 'mdl_target', status: 'draft', current_draft_revision_id: 'rev_1', current_draft_content: { title: '目标内容' }, workflow_status: 'draft', current_published_revision_id: null, referenced_assets: {}, created_by: 'usr_1', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }], fields: [{ key: 'title', display_name: '标题', type: 'single_line_text', constraints: {}, children: [] }], next_cursor: null })
    render(<DynamicContentForm fields={[field('single_relation', { constraints: { target_model_id: 'mdl_target' } })]} content={{}} onChange={onChange} />)
    await waitFor(() => expect(api.listEntries).toHaveBeenCalledWith('mdl_target', { status: 'draft', limit: 100, cursor: undefined }))
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'single_relation' }))
    fireEvent.click(await screen.findByText('目标内容（ent_target）'))
    expect(onChange).toHaveBeenCalledWith({ single_relation: 'ent_target' })
  })

  it('只读状态使用 JSON 文本预览，抽屉内不可编辑', async () => {
    render(<DynamicContentForm fields={[field('json')]} content={{ json: { value: 1 } }} onChange={vi.fn()} disabled />)
    expect(await screen.findByRole('group', { name: 'json' })).toHaveTextContent('value')
    expect(screen.getByRole('button', { name: '查看' })).toBeVisible()
    const drawer = await openComplexEditor('查看')
    const editor = await drawer.findByRole('textbox', { name: 'json' })
    expect(editor).toHaveAttribute('contenteditable', 'false')
    expect(drawer.getByRole('button', { name: '格式化 JSON' })).toBeDisabled()
  })

  it('只读状态打开富文本抽屉', async () => {
    render(<DynamicContentForm fields={[field('rich_text')]} content={{ rich_text: '<p>只读正文</p>' }} onChange={vi.fn()} disabled />)
    expect(screen.getByText('只读正文')).toBeVisible()
    const drawer = await openComplexEditor('查看')
    await waitFor(() => expect(document.querySelector('.rich-text-editor.is-disabled')).toBeTruthy())
    expect(drawer).toBeTruthy()
  })

  it('富文本媒体摘要在预览中显示占位', () => {
    render(<DynamicContentForm fields={[field('rich_text')]} content={{ rich_text: '<img data-asset-id="ast_cover" alt="封面">' }} onChange={vi.fn()} referencedAssets={{ ast_cover: { id: 'ast_cover', filename: 'cover.png', mime_type: 'image/png', size: 123, status: 'available', preview_kind: 'image' } }} />)
    expect(screen.getByText('[图片]')).toBeVisible()
  })

  it('可重复分组递归渲染媒体选择器', () => {
    const media = field('single_media', { id: 'cover', key: 'cover', display_name: '封面' })
    const group = field('repeatable_group', { id: 'slides', key: 'slides', display_name: '轮播项', children: [media] })
    render(<DynamicContentForm fields={[group]} content={{ slides: [{}] }} onChange={vi.fn()} canSelectAssets canUploadAssets />)
    expect(screen.getByText('第 1 项')).toBeVisible()
    expect(screen.getByRole('button', { name: '封面 选择素材' })).toBeEnabled()
  })

  it('字段标题、类型信息和说明与输入控件关联', () => {
    render(<DynamicContentForm fields={[field('single_line_text', { display_name: '文章标题', description: '用于列表和详情页展示' })]} content={{}} onChange={vi.fn()} />)
    const input = screen.getByRole('textbox', { name: '文章标题' })
    expect(input).toHaveAccessibleDescription('single_line_text · 单行文本 用于列表和详情页展示')
  })
})
