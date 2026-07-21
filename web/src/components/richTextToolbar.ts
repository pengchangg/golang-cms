import type { Editor } from '@tiptap/react'

const emptyToolbarState = {
  heading: 0, bold: false, italic: false, underline: false, strike: false, code: false,
  bulletList: false, orderedList: false, blockquote: false, codeBlock: false, canUndo: false, canRedo: false,
}

export function richTextToolbarState(editor: Editor | null) {
  if (!editor?.isInitialized || editor.isDestroyed) return emptyToolbarState
  return {
    heading: editor.isActive('heading') ? editor.getAttributes('heading').level as number : 0,
    bold: editor.isActive('bold'),
    italic: editor.isActive('italic'),
    underline: editor.isActive('underline'),
    strike: editor.isActive('strike'),
    code: editor.isActive('code'),
    bulletList: editor.isActive('bulletList'),
    orderedList: editor.isActive('orderedList'),
    blockquote: editor.isActive('blockquote'),
    codeBlock: editor.isActive('codeBlock'),
    canUndo: editor.can().undo(),
    canRedo: editor.can().redo(),
  }
}
