import { EditorContent, useEditor, useEditorState } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { Button, Select, Space } from 'antd'
import { useEffect, useEffectEvent, useRef } from 'react'

import { normalizeRichText, richTextFromEditor, richTextToEditor } from './richText'

const extensions = [
  StarterKit.configure({ horizontalRule: false, link: false }),
]

export function RichTextEditor({ value, onChange, disabled, label }: { value: unknown; onChange: (value: unknown) => void; disabled: boolean; label: string }) {
  const externalRef = useRef(JSON.stringify(normalizeRichText(value)))
  const emitChange = useEffectEvent((next: unknown) => onChange(next))
  const editor = useEditor({
    extensions,
    content: richTextToEditor(value),
    editable: !disabled,
  })

  useEffect(() => {
    if (!editor) return
    const handleUpdate = () => {
      const next = richTextFromEditor(current.getJSON())
      externalRef.current = JSON.stringify(next)
      emitChange(next)
    }
    const current = editor
    current.on('update', handleUpdate)
    return () => { current.off('update', handleUpdate) }
  }, [editor])

  useEffect(() => { editor?.setEditable(!disabled) }, [disabled, editor])
  useEffect(() => {
    if (!editor) return
    const serialized = JSON.stringify(normalizeRichText(value))
    if (serialized !== externalRef.current) {
      externalRef.current = serialized
      editor.commands.setContent(richTextToEditor(JSON.parse(serialized)), { emitUpdate: false })
    }
  }, [editor, value])

  const state = useEditorState({
    editor,
    selector: ({ editor: current }) => ({
      heading: current?.isActive('heading') ? current.getAttributes('heading').level as number : 0,
      bold: current?.isActive('bold') ?? false,
      italic: current?.isActive('italic') ?? false,
      underline: current?.isActive('underline') ?? false,
      strike: current?.isActive('strike') ?? false,
      code: current?.isActive('code') ?? false,
      bulletList: current?.isActive('bulletList') ?? false,
      orderedList: current?.isActive('orderedList') ?? false,
      blockquote: current?.isActive('blockquote') ?? false,
      codeBlock: current?.isActive('codeBlock') ?? false,
      canUndo: current?.can().undo() ?? false,
      canRedo: current?.can().redo() ?? false,
    }),
  })

  if (!editor) return null
  const run = (command: () => boolean) => { command(); editor.commands.focus() }
  return <div className={`rich-text-editor${disabled ? ' is-disabled' : ''}`} aria-label={`${label} 富文本编辑器`}>
    <div className="rich-text-toolbar" role="toolbar" aria-label={`${label} 格式工具栏`}>
      <Select aria-label="段落格式" size="small" disabled={disabled} value={state.heading ? `h${state.heading}` : 'paragraph'} onChange={(format) => run(() => format === 'paragraph' ? editor.chain().focus().setParagraph().run() : editor.chain().focus().toggleHeading({ level: Number(format.slice(1)) as 1 | 2 | 3 | 4 | 5 | 6 }).run())} options={[{ value: 'paragraph', label: '正文' }, ...Array.from({ length: 6 }, (_, index) => ({ value: `h${index + 1}`, label: `标题 ${index + 1}` }))]} />
      <Space.Compact>
        <Button size="small" disabled={disabled} type={state.bold ? 'primary' : 'default'} aria-label="粗体" onClick={() => run(() => editor.chain().focus().toggleBold().run())}><strong>B</strong></Button>
        <Button size="small" disabled={disabled} type={state.italic ? 'primary' : 'default'} aria-label="斜体" onClick={() => run(() => editor.chain().focus().toggleItalic().run())}><em>I</em></Button>
        <Button size="small" disabled={disabled} type={state.underline ? 'primary' : 'default'} aria-label="下划线" onClick={() => run(() => editor.chain().focus().toggleUnderline().run())}><u>U</u></Button>
        <Button size="small" disabled={disabled} type={state.strike ? 'primary' : 'default'} aria-label="删除线" onClick={() => run(() => editor.chain().focus().toggleStrike().run())}><s>S</s></Button>
        <Button size="small" disabled={disabled} type={state.code ? 'primary' : 'default'} aria-label="行内代码" onClick={() => run(() => editor.chain().focus().toggleCode().run())}><code>&lt;/&gt;</code></Button>
      </Space.Compact>
      <Space.Compact>
        <Button size="small" disabled={disabled} type={state.bulletList ? 'primary' : 'default'} onClick={() => run(() => editor.chain().focus().toggleBulletList().run())}>项目列表</Button>
        <Button size="small" disabled={disabled} type={state.orderedList ? 'primary' : 'default'} onClick={() => run(() => editor.chain().focus().toggleOrderedList().run())}>编号列表</Button>
        <Button size="small" disabled={disabled} type={state.blockquote ? 'primary' : 'default'} onClick={() => run(() => editor.chain().focus().toggleBlockquote().run())}>引用</Button>
        <Button size="small" disabled={disabled} type={state.codeBlock ? 'primary' : 'default'} onClick={() => run(() => editor.chain().focus().toggleCodeBlock().run())}>代码块</Button>
      </Space.Compact>
      <Space.Compact>
        <Button size="small" disabled={disabled || !state.canUndo} onClick={() => run(() => editor.chain().focus().undo().run())}>撤销</Button>
        <Button size="small" disabled={disabled || !state.canRedo} onClick={() => run(() => editor.chain().focus().redo().run())}>重做</Button>
      </Space.Compact>
    </div>
    <EditorContent editor={editor} />
  </div>
}
