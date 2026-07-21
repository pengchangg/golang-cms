import { Button, Form } from 'antd'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { FieldFormValues } from '../modelDesigner'
import { FieldConfig } from './ModelDesignerPage'

afterEach(cleanup)

function FieldForm({ onFinish }: { onFinish: (values: FieldFormValues) => void }) {
  const [form] = Form.useForm<FieldFormValues>()
  return <Form form={form} initialValues={{
    key: 'details', display_name: '详情', type: 'object', required: false, has_default: false,
    initial_children: [{ key: 'item', display_name: '子字段', type: 'single_line_text', required: false, has_default: false }],
  }} onFinish={onFinish}>
    <FieldConfig form={form} path={[]} watchPath={[]} depth={0} editing={false} relationOptions={[]} reloadRelations={vi.fn()} />
    <Button htmlType="submit">提交</Button>
  </Form>
}

describe('模型字段设计器表单', () => {
  it('初始子字段的稳定标识和显示名称互不覆盖', async () => {
    const onFinish = vi.fn()
    render(<FieldForm onFinish={onFinish} />)

    const keys = await screen.findAllByLabelText('稳定标识')
    const names = screen.getAllByLabelText('显示名称')
    await userEvent.clear(keys[1])
    await userEvent.type(keys[1], 'title')
    await userEvent.clear(names[1])
    await userEvent.type(names[1], '标题')
    expect(keys[1]).toHaveValue('title')
    expect(names[1]).toHaveValue('标题')

    await userEvent.click(screen.getByRole('button', { name: /提\s*交/ }))
    expect(onFinish).toHaveBeenCalledWith(expect.objectContaining({
      initial_children: [expect.objectContaining({ key: 'title', display_name: '标题' })],
    }))
  }, 15_000)
})
