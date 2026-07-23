import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '../api/client'
import type { Principal } from '../api/types'
import { TransferActions } from './TransferActions'

const principal: Principal = {
  user_id: 'usr_1', display_name: '内容编辑', email: null, auth_method: 'local', is_emergency_admin: false, has_high_risk_role: false, system_permissions: [],
  model_permissions: [{ model_id: 'mdl_1', permissions: ['content.view', 'content.create'] }],
  config_namespace_permissions: [],
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('同步 CSV 操作', () => {
  it('直接上传 CSV，成功后清空并刷新内容列表', async () => {
    const importCSV = vi.spyOn(api, 'importCSV').mockResolvedValue({ imported: 2 })
    const onImported = vi.fn()
    render(<TransferActions principal={principal} modelId="mdl_1" exportQuery={{}} onImported={onImported} />)

    await userEvent.click(screen.getByRole('button', { name: '导入 CSV' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/最多 1000 行、10 MiB/)).toBeInTheDocument()
    const file = new File(['title\n示例'], 'entries.csv', { type: 'text/csv' })
    await userEvent.upload(dialog.querySelector('input[type="file"]') as HTMLInputElement, file)
    await userEvent.click(within(dialog).getByRole('button', { name: '直接导入' }))

    await waitFor(() => expect(importCSV).toHaveBeenCalledWith('mdl_1', file))
    await waitFor(() => expect(onImported).toHaveBeenCalledOnce())
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('拒绝超过 10 MiB 的导入文件', async () => {
    const importCSV = vi.spyOn(api, 'importCSV')
    render(<TransferActions principal={principal} modelId="mdl_1" exportQuery={{}} onImported={vi.fn()} />)
    await userEvent.click(screen.getByRole('button', { name: '导入 CSV' }))
    const dialog = await screen.findByRole('dialog')
    const oversized = new File(['x'], 'huge.csv', { type: 'text/csv' })
    Object.defineProperty(oversized, 'size', { value: 10 * 1024 * 1024 + 1 })

    await userEvent.upload(dialog.querySelector('input[type="file"]') as HTMLInputElement, oversized)

    expect(importCSV).not.toHaveBeenCalled()
    expect(within(dialog).getByRole('button', { name: '直接导入' })).toBeDisabled()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('按当前筛选同步导出并释放临时下载地址', async () => {
    const blob = new Blob(['title\n示例'], { type: 'text/csv' })
    const exportCSV = vi.spyOn(api, 'exportCSV').mockResolvedValue(blob)
    const createObjectURL = vi.fn().mockReturnValue('blob:export')
    const revokeObjectURL = vi.fn()
    vi.stubGlobal('URL', { ...URL, createObjectURL, revokeObjectURL })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    render(<TransferActions principal={principal} modelId="mdl_1" exportQuery={{ workflow_status: 'draft', sort: '-title' }} onImported={vi.fn()} />)

    await userEvent.click(screen.getByRole('button', { name: '按当前筛选导出' }))

    await waitFor(() => expect(exportCSV).toHaveBeenCalledWith('mdl_1', { workflow_status: 'draft', sort: '-title' }))
    expect(createObjectURL).toHaveBeenCalledWith(blob)
    expect(click).toHaveBeenCalledOnce()
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:export')
  })
})
