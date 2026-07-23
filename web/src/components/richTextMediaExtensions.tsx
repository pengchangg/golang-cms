import { Node } from '@tiptap/core'
import { ReactNodeViewRenderer } from '@tiptap/react'

import { RichTextMediaView, type RichTextMediaKind } from './richTextMedia'

function createRichTextMediaExtension(kind: RichTextMediaKind) {
  return Node.create({
    name: kind,
    group: 'block',
    atom: true,
    draggable: true,
    selectable: true,
    addAttributes() {
      return {
        asset_id: { default: '' },
        ...(kind === 'image' ? { alt: { default: '' } } : {}),
      }
    },
    parseHTML() { return [{ tag: `div[data-rich-text-${kind}]` }] },
    renderHTML({ HTMLAttributes }) { return ['div', { ...HTMLAttributes, [`data-rich-text-${kind}`]: '' }] },
    addNodeView() { return ReactNodeViewRenderer(RichTextMediaView) },
  })
}

export const richTextMediaExtensions = [
  createRichTextMediaExtension('image'),
  createRichTextMediaExtension('audio'),
  createRichTextMediaExtension('video'),
]
