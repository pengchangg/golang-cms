const containerTypes = new Set(['doc', 'paragraph', 'heading', 'code_block', 'bullet_list', 'ordered_list', 'list_item', 'blockquote'])
const markTypes = new Set(['bold', 'italic', 'underline', 'strike', 'code'])

export interface RichTextNode {
  type: string
  attrs?: { level?: number }
  content?: RichTextNode[]
  text?: string
  marks?: Array<{ type: string }>
}

const editorTypeByBackendType: Record<string, string> = {
  code_block: 'codeBlock',
  bullet_list: 'bulletList',
  ordered_list: 'orderedList',
  list_item: 'listItem',
  hard_break: 'hardBreak',
}
const backendTypeByEditorType = Object.fromEntries(Object.entries(editorTypeByBackendType).map(([backend, editor]) => [editor, backend]))

export function normalizeRichText(value: unknown): RichTextNode {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return { type: 'doc', content: [] }
  const source = value as RichTextNode
  const type = typeof source.type === 'string' ? source.type : 'doc'
  if (type === 'text') {
    const marks = source.marks?.filter((mark) => markTypes.has(mark.type)).map((mark) => ({ type: mark.type }))
    return { type, text: typeof source.text === 'string' ? source.text : '', ...(marks?.length ? { marks } : {}) }
  }
  if (type === 'hard_break') return { type }
  if (!containerTypes.has(type)) return { type: 'paragraph', content: [] }
  const content = Array.isArray(source.content) ? source.content.map(normalizeRichText) : []
  if (type === 'heading') {
    const level = source.attrs?.level
    return { type, attrs: { level: typeof level === 'number' && level >= 1 && level <= 6 ? level : 2 }, content }
  }
  return { type, content }
}

export function richTextToEditor(value: unknown): RichTextNode {
  const node = normalizeRichText(value)
  return {
    ...node,
    type: editorTypeByBackendType[node.type] ?? node.type,
    ...(node.content ? { content: node.content.map(richTextToEditor) } : {}),
  }
}

export function richTextFromEditor(value: unknown): RichTextNode {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return normalizeRichText(value)
  const source = value as RichTextNode
  return normalizeRichText({
    ...source,
    type: backendTypeByEditorType[source.type] ?? source.type,
    ...(source.content ? { content: source.content.map(richTextFromEditor) } : {}),
  })
}
