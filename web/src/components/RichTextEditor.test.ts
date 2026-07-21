import type { Editor } from '@tiptap/react'
import { describe, expect, it, vi } from 'vitest'

import { richTextToolbarState } from './richTextToolbar'

describe('富文本编辑器工具栏状态', () => {
  it('编辑器尚未初始化时不读取命令状态', () => {
    const can = vi.fn(() => { throw new Error('未初始化时不能读取命令') })
    const editor = { isInitialized: false, isDestroyed: true, can } as unknown as Editor

    expect(richTextToolbarState(editor)).toMatchObject({ heading: 0, bold: false, canUndo: false, canRedo: false })
    expect(can).not.toHaveBeenCalled()
  })
})
