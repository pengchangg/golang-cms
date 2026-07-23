import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import APIExplorerPage from './APIExplorerPage'

const apiKey = `cmsk_abcdefghijkl_${'x'.repeat(43)}`
const model = {
  id: 'mdl_articles', key: 'articles', display_name: '文章', description: '官网文章', updated_at: '2026-07-22T08:00:00Z',
  fields: [
    { id: 'fld_score', key: 'score', display_name: '评分', description: '', type: 'integer', required: false, constraints: { unique: false, filterable: true, sortable: true }, children: [] },
    { id: 'fld_author', key: 'author', display_name: '作者', description: '', type: 'single_relation', required: false, constraints: { unique: false, filterable: false, sortable: false, target_model_key: 'authors' }, children: [] },
  ],
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('客户端调试页面', () => {
  it('发现模型并按字段能力构造真实内容查询', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(Response.json({ items: [{ id: model.id, key: model.key, display_name: model.display_name, description: model.description, updated_at: model.updated_at }] }, { headers: { 'X-Request-ID': 'req_models' } }))
      .mockResolvedValueOnce(Response.json(model, { headers: { 'X-Request-ID': 'req_model' } }))
      .mockResolvedValueOnce(Response.json({ items: [], next_cursor: null }, { headers: { 'X-Request-ID': 'req_entries', ETag: '"sha256-entries"' } }))
    vi.stubGlobal('fetch', fetchMock)
    render(<APIExplorerPage />)

    fireEvent.change(screen.getByLabelText('API Key'), { target: { value: apiKey } })
    fireEvent.click(screen.getByRole('button', { name: /连接并发现模型/ }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    fireEvent.click(screen.getByText('模型定义'))
    fireEvent.mouseDown(screen.getByLabelText('内容模型'))
    fireEvent.click(await screen.findByText('文章 · articles'))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))

    fireEvent.click(screen.getByText('内容列表'))
    fireEvent.mouseDown(screen.getByLabelText('过滤字段'))
    fireEvent.click(screen.getByText('评分 · score'))
    fireEvent.mouseDown(screen.getByLabelText('过滤操作符'))
    fireEvent.click(screen.getByText('gte'))
    fireEvent.change(screen.getByLabelText('过滤值'), { target: { value: '80' } })
    fireEvent.mouseDown(screen.getByLabelText('展开关联'))
    fireEvent.click(screen.getByText('作者 · author'))
    fireEvent.click(screen.getByRole('button', { name: /发送请求/ }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3))
    expect(fetchMock.mock.calls[2][0]).toContain('/api/content/v1/models/articles/entries?')
    const url = new URL(String(fetchMock.mock.calls[2][0]), window.location.origin)
    expect(url.searchParams.get('filter')).toBe('{"score":{"gte":80}}')
    expect(url.searchParams.get('expand')).toBe('author')
    expect(await screen.findByText('req_entries')).toBeVisible()
    expect(screen.getByText('"sha256-entries"')).toBeVisible()
    expect(document.body.textContent).not.toContain(apiKey)
    expect(localStorage).toHaveLength(0)
    expect(sessionStorage).toHaveLength(0)
  }, 20_000)

  it('原始查询模式保留服务端错误响应和 request ID', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(Response.json({ items: [{ id: model.id, key: model.key, display_name: model.display_name, description: model.description, updated_at: model.updated_at }] }))
      .mockResolvedValueOnce(Response.json(model))
      .mockResolvedValueOnce(Response.json({ error: { code: 'invalid_query', message: '内容查询无效', request_id: 'req_invalid', details: [] } }, { status: 400, headers: { 'X-Request-ID': 'req_invalid' } }))
    vi.stubGlobal('fetch', fetchMock)
    render(<APIExplorerPage />)
    fireEvent.change(screen.getByLabelText('API Key'), { target: { value: apiKey } })
    fireEvent.click(screen.getByRole('button', { name: /连接并发现模型/ }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    fireEvent.click(screen.getByText('模型定义'))
    fireEvent.mouseDown(screen.getByLabelText('内容模型'))
    fireEvent.click(await screen.findByText('文章 · articles'))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    fireEvent.click(screen.getByText('内容列表'))
    fireEvent.click(await screen.findByRole('checkbox', { name: '原始查询参数模式' }))
    fireEvent.change(screen.getByLabelText('原始查询参数'), { target: { value: 'sort=unknown' } })
    fireEvent.click(screen.getByRole('button', { name: /发送请求/ }))

    expect(await screen.findByText(/invalid_query/)).toBeVisible()
    expect(screen.getAllByText('req_invalid').length).toBeGreaterThan(0)
    expect(screen.getAllByText('400').length).toBeGreaterThan(0)
  })

  it('构造配置命名空间和配置单项请求', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(Response.json({ maintenance_enabled: false }, { headers: { 'X-Request-ID': 'req_config' } }))
      .mockResolvedValueOnce(Response.json({ item_key: 'home.hero', value_type: 'string', value: '欢迎', revision_id: 'crv_1', revision_number: 1, published_at: '2026-07-23T00:00:00Z' }, { headers: { 'X-Request-ID': 'req_config_item' } }))
    vi.stubGlobal('fetch', fetchMock)
    render(<APIExplorerPage />)
    fireEvent.change(screen.getByLabelText('API Key'), { target: { value: apiKey } })

    fireEvent.click(screen.getByText('配置命名空间'))
    fireEvent.change(screen.getByLabelText('配置命名空间 key'), { target: { value: 'website' } })
    fireEvent.click(screen.getByRole('button', { name: /发送请求/ }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(fetchMock.mock.calls[0][0]).toBe('/api/content/v1/configurations/website')
    expect(await screen.findByText('req_config')).toBeVisible()

    fireEvent.click(screen.getByText('配置单项'))
    fireEvent.change(screen.getByLabelText('配置项 key'), { target: { value: 'home.hero' } })
    fireEvent.click(screen.getByRole('button', { name: /发送请求/ }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(fetchMock.mock.calls[1][0]).toBe('/api/content/v1/configurations/website/home.hero')
    expect(await screen.findByText('req_config_item')).toBeVisible()
  })
})
