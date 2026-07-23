import { describe, expect, it } from 'vitest'

import { hydrateRichTextHTML, isRichTextImageFile, normalizeRichTextHTML, richTextMediaHTML, richTextPlainText } from './richText'

describe('richText HTML helpers', () => {
  it('持久化时剥离临时 src 并保留素材引用', () => {
    const html = `<p>正文</p><img data-asset-id="ast_image" alt="封面" src="blob:https://example/1" width="320" title="x"><audio data-asset-id="ast_audio" src="/tmp.mp3" controls></audio>`
    expect(normalizeRichTextHTML(html)).toBe('<p>正文</p><img data-asset-id="ast_image" alt="封面" width="320"><audio data-asset-id="ast_audio" controls=""></audio>')
  })

  it('缺少素材 ID 的媒体节点会被删除', () => {
    expect(normalizeRichTextHTML('<img src="https://evil.example/a.png" alt="x"><p>ok</p>')).toBe('<p>ok</p>')
  })

  it('编辑器会话可注入预览地址', () => {
    const hydrated = hydrateRichTextHTML('<img data-asset-id="ast_1" alt="">', (id) => id === 'ast_1' ? '/api/admin/v1/assets/ast_1/preview' : undefined)
    expect(hydrated).toContain('src="/api/admin/v1/assets/ast_1/preview"')
    expect(normalizeRichTextHTML(hydrated)).toBe('<img data-asset-id="ast_1" alt="">')
  })

  it('拖拽缩放产生的 style 尺寸会转成 width/height 属性', () => {
    const html = `<img data-asset-id="ast_image" alt="封面" src="/preview" style="width: 480px; height: 270px;">`
    expect(normalizeRichTextHTML(html)).toBe('<img data-asset-id="ast_image" alt="封面" width="480" height="270">')
  })

  it('生成媒体 HTML 片段', () => {
    expect(richTextMediaHTML('image', 'ast_1', { alt: 'a"b', width: 200 })).toBe('<img data-asset-id="ast_1" alt="a&quot;b" width="200">')
    expect(richTextMediaHTML('video', 'ast_2')).toBe('<video data-asset-id="ast_2" controls></video>')
  })

  it('识别富文本允许的图片文件类型', () => {
    expect(isRichTextImageFile(new File([], 'a.png', { type: 'image/png' }))).toBe(true)
    expect(isRichTextImageFile(new File([], 'a.avif', { type: '' }))).toBe(true)
    expect(isRichTextImageFile(new File([], 'a.pdf', { type: 'application/pdf' }))).toBe(false)
  })

  it('摘要提取纯文本与媒体占位', () => {
    expect(richTextPlainText('<h2>标题</h2><p>摘要正文</p><img data-asset-id="ast_1" alt="">')).toBe('标题\n摘要正文\n[图片]')
    expect(richTextPlainText('')).toBe('')
    expect(richTextPlainText(null)).toBe('')
  })
})
