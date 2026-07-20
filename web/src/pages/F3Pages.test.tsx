import { act, cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import * as client from '../api/client'
import type { Job, Principal } from '../api/types'
import AssetsPage from './AssetsPage'
import JobDetailPage from './JobDetailPage'
import JobsPage from './JobsPage'

const principal: Principal = { user_id: 'usr_1', display_name: '测试用户', email: null, auth_method: 'local', system_permissions: ['assets.view', 'assets.upload', 'assets.archive', 'transfers.execute', 'transfers.download'], model_permissions: [{ model_id: 'mdl_1', permissions: ['content.view', 'content.create'] }] }
const job: Job = { id: 'job_1', type: 'csv_import', status: 'failed', model_id: 'mdl_1', progress: 100, attempt: 1, max_attempts: 3, cancel_requested_at: null, error_code: 'validation_failed', error_message: '一行错误', created_by: 'usr_1', created_at: '2026-07-19T08:00:00Z', started_at: '2026-07-19T08:01:00Z', finished_at: '2026-07-19T08:02:00Z', expires_at: '2099-07-26T08:00:00Z' }

afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks() })

describe('F3 页面', () => {
  it('素材列表展示状态并提供上传入口', async () => {
    vi.spyOn(client.api, 'listAssets').mockResolvedValue({ items: [{ id: 'ast_1', filename: '封面.png', mime_type: 'image/png', size: 1024, sha256: 'a'.repeat(64), etag: 'etag', status: 'available', created_by: 'usr_1', created_at: '2026-07-19T08:00:00Z', confirmed_at: '2026-07-19T08:00:00Z', archived_at: null }], next_cursor: null })
    render(<MemoryRouter><AssetsPage principal={principal} /></MemoryRouter>)
    expect(await screen.findByText('封面.png')).toBeVisible()
    expect(screen.getByText('可用')).toBeVisible()
    expect(screen.getByRole('button', { name: '上传素材' })).toBeEnabled()
  })

  it('失败导入任务展示进度、错误分页和报告下载', async () => {
    vi.spyOn(client.api, 'getJob').mockResolvedValue(job)
    vi.spyOn(client.api, 'listJobErrors').mockResolvedValue({ items: [{ row: 2, field: 'title', code: 'validation_failed', message: '不能为空' }], next_cursor: null, errors_truncated: false })
    render(<MemoryRouter initialEntries={['/jobs/job_1']}><Routes><Route path="/jobs/:jobId" element={<JobDetailPage principal={principal} />} /></Routes></MemoryRouter>)
    expect(await screen.findByText('不能为空')).toBeVisible()
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '100')
    expect(screen.getByRole('link', { name: '下载错误报告' })).toHaveAttribute('href', '/api/admin/v1/jobs/job_1/files/errors')
  })

  it('任务列表每 5 秒轮询活动任务，进入终态后停止', async () => {
    vi.useFakeTimers()
    const listJobs = vi.spyOn(client.api, 'listJobs')
      .mockResolvedValueOnce({ items: [{ ...job, status: 'queued', progress: 0, error_code: null, error_message: null }], next_cursor: null })
      .mockResolvedValueOnce({ items: [{ ...job, status: 'succeeded', progress: 100, error_code: null, error_message: null }], next_cursor: null })
    render(<MemoryRouter><JobsPage /></MemoryRouter>)
    await act(async () => { await Promise.resolve() })
    expect(listJobs).toHaveBeenCalledTimes(1)

    await act(async () => { await vi.advanceTimersByTimeAsync(5_000) })
    expect(screen.getByText('已完成')).toBeVisible()
    expect(listJobs).toHaveBeenCalledTimes(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(listJobs).toHaveBeenCalledTimes(2)
  })

  it('任务详情轮询后及时展示下载，并在卸载时清理轮询', async () => {
    vi.useFakeTimers()
    const getJob = vi.spyOn(client.api, 'getJob')
      .mockResolvedValueOnce({ ...job, type: 'csv_export', status: 'running', progress: 50, error_code: null, error_message: null })
      .mockResolvedValueOnce({ ...job, type: 'csv_export', status: 'succeeded', error_code: null, error_message: null })
    const view = render(<MemoryRouter initialEntries={['/jobs/job_1']}><Routes><Route path="/jobs/:jobId" element={<JobDetailPage principal={principal} />} /></Routes></MemoryRouter>)
    await act(async () => { await Promise.resolve() })
    await act(async () => { await vi.advanceTimersByTimeAsync(5_000) })
    expect(screen.getByRole('link', { name: '下载导出结果' })).toHaveAttribute('href', '/api/admin/v1/jobs/job_1/files/result')
    view.unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(getJob).toHaveBeenCalledTimes(2)
  })

  it('任务详情轮询到失败终态后展示错误并停止', async () => {
    vi.useFakeTimers()
    const getJob = vi.spyOn(client.api, 'getJob')
      .mockResolvedValueOnce({ ...job, status: 'running', progress: 50, error_code: null, error_message: null })
      .mockResolvedValueOnce(job)
    render(<MemoryRouter initialEntries={['/jobs/job_1']}><Routes><Route path="/jobs/:jobId" element={<JobDetailPage principal={principal} />} /></Routes></MemoryRouter>)
    await act(async () => { await Promise.resolve() })
    await act(async () => { await vi.advanceTimersByTimeAsync(5_000) })
    expect(screen.getByText('一行错误')).toBeVisible()
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(getJob).toHaveBeenCalledTimes(2)
  })

  it('超限文件在读取 arrayBuffer 前被拒绝', async () => {
    const arrayBuffer = vi.fn()
    const oversized = { name: 'huge.csv', size: client.ASSET_MAX_BYTES + 1, arrayBuffer } as unknown as File
    await expect(client.hashUploadFile(oversized)).rejects.toThrow(`超过 ${client.ASSET_MAX_LABEL} 上限`)
    expect(arrayBuffer).not.toHaveBeenCalled()
    expect(client.ASSET_MAX_BYTES).toBeLessThanOrEqual(100 * 1024 * 1024)
  })
})
