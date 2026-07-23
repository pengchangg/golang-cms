import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { JsonEditor } from './JsonEditor'

afterEach(cleanup)

describe('JSON 文本编辑器', () => {
  it('编辑合法 JSON 后回写解析结果', async () => {
    const onChange = vi.fn()
    render(<JsonEditor value={{ title: '旧标题' }} onChange={onChange} label="页面配置" disabled={false} variant="full" />)

    const editor = await screen.findByRole('textbox', { name: '页面配置 JSON 编辑器' })
    await userEvent.click(editor)
    await userEvent.keyboard('{Control>}a{/Control}')
    await userEvent.paste('{"title":"新标题"}')

    await waitFor(() => expect(onChange).toHaveBeenCalledWith({ title: '新标题' }))
  })

  it('非法 JSON 时报告无效并显示错误', async () => {
    const onValidityChange = vi.fn()
    render(<JsonEditor value={{ ok: true }} onChange={vi.fn()} label="配置" disabled={false} variant="full" onValidityChange={onValidityChange} />)

    const editor = await screen.findByRole('textbox', { name: '配置 JSON 编辑器' })
    await userEvent.click(editor)
    await userEvent.keyboard('{Control>}a{/Control}')
    await userEvent.paste('{')

    await waitFor(() => expect(onValidityChange).toHaveBeenCalledWith(false))
    expect(await screen.findByText('JSON 格式无效')).toBeVisible()
  })

  it('预览态只读展示文本并报告有效', () => {
    const onValidityChange = vi.fn()
    render(<JsonEditor value={{ value: 1 }} onChange={vi.fn()} label="配置" disabled variant="preview" labelledBy="field-label" describedBy="field-description" onValidityChange={onValidityChange} />)

    expect(screen.getByRole('group')).toHaveAttribute('aria-labelledby', 'field-label')
    expect(screen.getByRole('group')).toHaveAttribute('aria-describedby', 'field-description')
    expect(screen.getByRole('group')).toHaveClass('is-preview')
    expect(screen.getByText(/"value": 1/)).toBeVisible()
    expect(onValidityChange).toHaveBeenCalledWith(true)
  })
})
