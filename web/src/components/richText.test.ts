import { describe, expect, it } from 'vitest'

import { normalizeRichText, richTextFromEditor, richTextToEditor } from './richText'

describe('富文本规范化', () => {
  it('补齐空容器并只保留后端支持的属性和标记', () => {
    expect(normalizeRichText({ type: 'doc', extra: true, content: [{ type: 'heading', attrs: { level: 9 }, content: [{ type: 'text', text: '标题', marks: [{ type: 'bold' }, { type: 'link' }] }] }, { type: 'paragraph' }] })).toEqual({
      type: 'doc',
      content: [
        { type: 'heading', attrs: { level: 2 }, content: [{ type: 'text', text: '标题', marks: [{ type: 'bold' }] }] },
        { type: 'paragraph', content: [] },
      ],
    })
  })

  it('无效根值转为空文档', () => {
    expect(normalizeRichText(null)).toEqual({ type: 'doc', content: [] })
  })

  it('在后端蛇形节点名和编辑器驼峰节点名之间双向转换', () => {
    const backend = { type: 'doc', content: [{ type: 'bullet_list', content: [{ type: 'list_item', content: [{ type: 'code_block', content: [{ type: 'text', text: 'code' }] }] }] }] }
    const editor = { type: 'doc', content: [{ type: 'bulletList', content: [{ type: 'listItem', content: [{ type: 'codeBlock', content: [{ type: 'text', text: 'code' }] }] }] }] }
    expect(richTextToEditor(backend)).toEqual(editor)
    expect(richTextFromEditor(editor)).toEqual(backend)
  })

  it('媒体节点只保留素材 ID 和图片替代文本', () => {
    const value = { type: 'doc', content: [
      { type: 'image', attrs: { asset_id: 'ast_image', alt: '封面', src: 'https://example.com/a.jpg' }, content: [] },
      { type: 'audio', attrs: { asset_id: 'ast_audio', mime_type: 'audio/mpeg' } },
      { type: 'video', attrs: { asset_id: 'ast_video', filename: 'a.mp4' } },
    ] }
    expect(normalizeRichText(value)).toEqual({ type: 'doc', content: [
      { type: 'image', attrs: { asset_id: 'ast_image', alt: '封面' } },
      { type: 'audio', attrs: { asset_id: 'ast_audio' } },
      { type: 'video', attrs: { asset_id: 'ast_video' } },
    ] })
  })
})
