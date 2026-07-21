import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'

import { api } from '../api/client'
import type { ContentEntry, ContentModel, Principal } from '../api/types'
import APIKeysPage from './APIKeysPage'
import EntriesPage from './EntriesPage'
import EntryEditorPage from './EntryEditorPage'

const now = '2026-07-19T08:00:00Z'
const field = { id: 'fld_title', key: 'title', display_name: '标题', description: '', type: 'single_line_text' as const, required: true, default_value: null, constraints: {}, children: [], status: 'active' as const, created_at: now, updated_at: now }
const model: ContentModel = { id: 'mdl_1', key: 'articles', display_name: '文章', description: '', status: 'active', fields: [field], created_at: now, updated_at: now }
const entry: ContentEntry = {
  id: 'ent_1', model_id: model.id, status: 'draft', current_draft_revision_id: 'rev_1', workflow_status: 'draft', current_published_revision_id: null,
  current_draft_content: { title: '旧标题' },
  referenced_assets: {},
  created_by: 'usr_1', created_at: now, updated_at: now,
  current_draft_revision: { id: 'rev_1', entry_id: 'ent_1', model_id: model.id, number: 1, content: { title: '旧标题' }, created_by: 'usr_1', created_at: now, workflow_status: 'draft', submitted_by: null, submitted_at: null },
  current_published_revision: null,
}

function principal(system: Principal['system_permissions'], permissions: Principal['model_permissions'][number]['permissions']): Principal {
  return { user_id: 'usr_2', display_name: '审核人', email: null, auth_method: 'sms', system_permissions: system, model_permissions: [{ model_id: model.id, permissions }] }
}

function renderEntry(value: Principal) {
  return render(<MemoryRouter initialEntries={['/content/mdl_1/ent_1']}><Routes><Route path="/content/:modelId/:entryId" element={<EntryEditorPage principal={value} />} /></Routes></MemoryRouter>)
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('工作流页面行为', () => {
  it('草稿存在未保存修改时禁用提交审核且不发送旧 Revision', async () => {
    vi.spyOn(api, 'getModel').mockResolvedValue(model)
    vi.spyOn(api, 'getEntry').mockResolvedValue(entry)
    vi.spyOn(api, 'listWorkflowEvents').mockResolvedValue({ items: [], next_cursor: null })
    const submit = vi.spyOn(api, 'submitEntry').mockResolvedValue(entry)
    renderEntry(principal(['models.view'], ['content.view', 'content.update', 'content.submit']))

    const title = await screen.findByRole('textbox')
    await userEvent.clear(title)
    await userEvent.type(title, '新标题')

    expect(screen.getByRole('button', { name: '提交审核' })).toBeDisabled()
    expect(screen.getByText('有未保存的修改')).toBeVisible()
    expect(submit).not.toHaveBeenCalled()
  })

  it('没有 models.view 时仍展示审核内容并可执行工作流', async () => {
    const pending = { ...entry, workflow_status: 'pending_review' as const, current_draft_revision: { ...entry.current_draft_revision, workflow_status: 'pending_review' as const, submitted_by: 'usr_1', submitted_at: now } }
    const getModel = vi.spyOn(api, 'getModel')
    vi.spyOn(api, 'getEntry').mockResolvedValue(pending)
    vi.spyOn(api, 'listWorkflowEvents').mockResolvedValue({ items: [], next_cursor: null })
    const approve = vi.spyOn(api, 'approveEntry').mockResolvedValue({ ...pending, workflow_status: 'published', current_draft_revision: { ...pending.current_draft_revision, workflow_status: 'published' } })
    renderEntry(principal([], ['content.view', 'content.review', 'content.publish']))

    expect(await screen.findByText('无内容模型结构权限')).toBeVisible()
    expect(screen.getByLabelText('只读内容数据')).toHaveTextContent('旧标题')
    expect(screen.getByRole('button', { name: '保存草稿' })).toBeDisabled()
    expect(getModel).not.toHaveBeenCalled()

    await userEvent.click(screen.getByRole('button', { name: '通过并发布' }))
    await waitFor(() => expect(approve).toHaveBeenCalledWith('mdl_1', 'ent_1', 'rev_1'))
  })

  it('内容列表使用 next_cursor 请求下一页', async () => {
    const getModel = vi.spyOn(api, 'getModel')
    const details = { body: 'x'.repeat(100) }
    const fields = [
      { key: 'title', display_name: '标题', type: 'single_line_text' as const, constraints: { filterable: true, sortable: true }, children: [] },
      { key: 'kind', display_name: '类型', type: 'single_select' as const, constraints: { enum_options: [{ value: 'news', label: '新闻' }], filterable: true, sortable: false }, children: [] },
      { key: 'details', display_name: '详情', type: 'json' as const, constraints: { filterable: false, sortable: false }, children: [] },
    ]
    const listedEntry = { ...entry, current_draft_content: { title: '旧标题', kind: 'news', details } }
    const listEntries = vi.spyOn(api, 'listEntries')
      .mockResolvedValueOnce({ items: [listedEntry], fields, next_cursor: 'entries_2' })
      .mockResolvedValueOnce({ items: [listedEntry], fields, next_cursor: 'entries_2' })
      .mockResolvedValueOnce({ items: [], fields: [], next_cursor: null })
    render(<MemoryRouter initialEntries={['/content/mdl_1']}><Routes><Route path="/content/:modelId" element={<EntriesPage principal={principal([], ['content.view'])} />} /></Routes></MemoryRouter>)
    expect(await screen.findByText('旧标题')).toBeVisible()
    expect(screen.getByText('新闻')).toBeVisible()
    expect(screen.getByText(`${JSON.stringify(details).slice(0, 77)}...`)).toBeVisible()
    expect(screen.getByRole('columnheader', { name: '标题' })).toBeVisible()
    const fieldSelect = screen.getByRole('combobox', { name: '过滤字段' })
    await userEvent.click(fieldSelect)
    expect(await screen.findByRole('option', { name: '标题' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '类型' })).toBeInTheDocument()
    await userEvent.keyboard('{Escape}')
    expect(getModel).not.toHaveBeenCalled()
    await userEvent.click(await screen.findByRole('button', { name: '下一页' }))
    await waitFor(() => expect(listEntries).toHaveBeenLastCalledWith('mdl_1', expect.objectContaining({ cursor: 'entries_2' })))
  })

  it('内容列表递归展示根媒体和 object/repeatable 嵌套素材且不请求素材详情', async () => {
    const media = (id: string, filename: string) => ({ id, filename, mime_type: 'image/png', size: 100, status: 'available' as const, preview_kind: 'image' as const })
    const mediaField = (key: string, display_name: string) => ({ key, display_name, type: 'single_media' as const, constraints: { filterable: false, sortable: false }, children: [] })
    const fields = [
      { ...mediaField('cover', '封面') },
      { ...mediaField('gallery', '图集'), type: 'multi_media' as const },
      { key: 'details', display_name: '详情', type: 'object' as const, constraints: { filterable: false, sortable: false }, children: [{ ...mediaField('poster', '海报'), type: 'multi_media' as const }] },
      { key: 'sections', display_name: '章节', type: 'repeatable_group' as const, constraints: { filterable: false, sortable: false }, children: [mediaField('illustration', '插图')] },
    ]
    const referenced_assets = Object.fromEntries(['cover', 'one', 'two', 'three', 'four', 'poster', 'nested'].map((id) => [id, media(id, `${id}.png`)]))
    vi.spyOn(api, 'listEntries').mockResolvedValue({ items: [{ ...entry, current_draft_content: { cover: 'cover', gallery: ['one', 'two', 'three', 'four'], details: { poster: ['one', 'two', 'three', 'four'] }, sections: [{ illustration: 'nested' }] }, referenced_assets }], fields, next_cursor: null })
    const getAsset = vi.spyOn(api, 'getAsset')
    render(<MemoryRouter initialEntries={['/content/mdl_1']}><Routes><Route path="/content/:modelId" element={<EntriesPage principal={principal([], ['content.view'])} />} /></Routes></MemoryRouter>)

    expect(await screen.findByText('cover.png')).toBeVisible()
    expect(screen.getAllByText('one.png').length).toBeGreaterThan(0)
    expect(screen.getByText('nested.png')).toBeVisible()
    const more = screen.getAllByText('+1 查看全部')
    expect(more).toHaveLength(2)
    expect(screen.getByText('cover.png')).toHaveAttribute('title', '预览 cover.png')
    expect(document.querySelector('img[src="/api/admin/v1/models/mdl_1/entries/ent_1/assets/cover/preview"]')).toBeInTheDocument()
    expect(getAsset).not.toHaveBeenCalled()

    await userEvent.click(more[0])
    expect(screen.getAllByText('four.png').length).toBeGreaterThan(0)
  })

  it('引用素材元数据缺失时显示不可用占位', async () => {
    const fields = [{ key: 'cover', display_name: '封面', type: 'single_media' as const, constraints: { filterable: false, sortable: false }, children: [] }]
    vi.spyOn(api, 'listEntries').mockResolvedValue({ items: [{ ...entry, current_draft_content: { cover: 'ast_missing' }, referenced_assets: {} }], fields, next_cursor: null })
    render(<MemoryRouter initialEntries={['/content/mdl_1']}><Routes><Route path="/content/:modelId" element={<EntriesPage principal={principal([], ['content.view'])} />} /></Routes></MemoryRouter>)
    expect(await screen.findByText('素材不可用')).toBeVisible()
  })

  it('API Key 列表使用 next_cursor 请求下一页', async () => {
    vi.spyOn(api, 'listModels').mockResolvedValue({ items: [] })
    const listKeys = vi.spyOn(api, 'listAPIKeys')
      .mockResolvedValueOnce({ items: [{ id: 'key_1', name: '读取', prefix: 'abcd', model_ids: [], status: 'active', expires_at: null, revoked_at: null, last_used_at: null, rotated_from_id: null, replaced_by_id: null, created_by: 'usr_1', created_at: now }], next_cursor: 'keys_2' })
      .mockResolvedValueOnce({ items: [], next_cursor: null })
    render(<MemoryRouter><APIKeysPage principal={principal(['api_keys.view'], [])} /></MemoryRouter>)
    await userEvent.click(await screen.findByRole('button', { name: '下一页' }))
    await waitFor(() => expect(listKeys).toHaveBeenLastCalledWith(undefined, 'keys_2'))
  })

  it('工作流事件使用 next_cursor 请求下一页', async () => {
    vi.spyOn(api, 'getModel').mockResolvedValue(model)
    vi.spyOn(api, 'getEntry').mockResolvedValue(entry)
    const listEvents = vi.spyOn(api, 'listWorkflowEvents')
      .mockResolvedValueOnce({ items: [{ id: 'evt_1', entry_id: entry.id, revision_id: 'rev_1', type: 'submitted', from_status: 'draft', to_status: 'pending_review', actor_id: 'usr_1', reason: null, occurred_at: now }], next_cursor: 'events_2' })
      .mockResolvedValueOnce({ items: [], next_cursor: null })
    renderEntry(principal(['models.view'], ['content.view']))
    await userEvent.click(await screen.findByRole('button', { name: '下一页事件' }))
    await waitFor(() => expect(listEvents).toHaveBeenLastCalledWith('mdl_1', 'ent_1', 'events_2'))
  })
})
