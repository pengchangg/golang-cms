import Placeholder from '@tiptap/extension-placeholder'
import { EditorContent, useEditor, useEditorState } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { Button, Divider, Input, Modal, Select, Space } from 'antd'
import { useEffect, useRef, useState } from 'react'

import { adminDownloadUrl } from '../api/client'
import type { Asset, AssetKind, ReferencedAsset } from '../api/types'
import { ASSETS_ENABLED } from '../config'
import { AssetPicker } from './AssetPicker'
import { normalizeRichText, richTextFromEditor, richTextToEditor } from './richText'
import { type RichTextMediaEnvironment, type RichTextMediaKind } from './richTextMedia'
import { RichTextMediaContext } from './richTextMediaContext'
import { richTextMediaExtensions } from './richTextMediaExtensions'
import { richTextToolbarState } from './richTextToolbar'

interface PendingMedia {
  asset: Asset
  kind: RichTextMediaKind
  position?: number
}

interface RichTextEditorProps {
  value: unknown
  onChange: (value: unknown) => void
  disabled: boolean
  label: string
  labelledBy?: string
  describedBy?: string
  canSelectAssets?: boolean
  canUploadAssets?: boolean
  referencedAssets?: Record<string, ReferencedAsset>
  modelId?: string
  entryId?: string
}

export function RichTextEditor({ value, onChange, disabled, label, labelledBy, describedBy, canSelectAssets = false, canUploadAssets = false, referencedAssets = {}, modelId, entryId }: RichTextEditorProps) {
  const [localAssets, setLocalAssets] = useState<Record<string, Asset>>({})
  const [pickerKind, setPickerKind] = useState<AssetKind | undefined>(undefined)
  const [replacePosition, setReplacePosition] = useState<number | undefined>(undefined)
  const [pendingImage, setPendingImage] = useState<PendingMedia | undefined>(undefined)
  const [alt, setAlt] = useState('')
  const externalRef = useRef(JSON.stringify(normalizeRichText(value)))
  const onChangeRef = useRef(onChange)
  const allAssets: Record<string, ReferencedAsset> = { ...referencedAssets, ...localAssets }
  const editor = useEditor({
    extensions: [StarterKit.configure({ horizontalRule: false, link: false }), Placeholder.configure({ placeholder: '开始输入正文' }), ...richTextMediaExtensions],
    content: richTextToEditor(value),
    editable: !disabled,
    editorProps: { attributes: { role: 'textbox', 'aria-multiline': 'true', ...(labelledBy ? { 'aria-labelledby': labelledBy, ...(describedBy ? { 'aria-describedby': describedBy } : {}) } : { 'aria-label': `${label} 富文本编辑器` }) } },
  })

  const mediaUrl = (assetID: string, action: 'preview' | 'download') => {
    if (!ASSETS_ENABLED) return undefined
    const encoded = encodeURIComponent(assetID)
    if (entryId && modelId && referencedAssets[assetID]) return adminDownloadUrl(`/models/${encodeURIComponent(modelId)}/entries/${encodeURIComponent(entryId)}/assets/${encoded}/${action}`)
    if (canSelectAssets) return adminDownloadUrl(`/assets/${encoded}/${action}`)
    return undefined
  }
  const environment: RichTextMediaEnvironment = {
    assets: allAssets,
    disabled,
    canSelect: ASSETS_ENABLED && canSelectAssets,
    previewUrl: (assetID) => mediaUrl(assetID, 'preview'),
    downloadUrl: (assetID) => mediaUrl(assetID, 'download'),
    replace: (kind, _editor, getPos) => {
      const position = getPos()
      if (typeof position !== 'number') return
      setReplacePosition(position)
      setPickerKind(kind)
    },
  }

  useEffect(() => { onChangeRef.current = onChange }, [onChange])
  useEffect(() => {
    if (!editor) return
    const emitEditorChange = () => {
      if (editor.isDestroyed) return
      const next = richTextFromEditor(editor.getJSON())
      externalRef.current = JSON.stringify(next)
      onChangeRef.current(next)
    }
    const handleUpdate = () => emitEditorChange()
    editor.on('update', handleUpdate)
    return () => { editor.off('update', handleUpdate) }
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

  const state = useEditorState({ editor, selector: ({ editor: current }) => richTextToolbarState(current) })
  if (!editor) return null
  const run = (command: () => boolean) => { command(); editor.commands.focus() }
  const insertMedia = (kind: RichTextMediaKind, asset: Asset, imageAlt = '', position = replacePosition) => {
    setLocalAssets((current) => ({ ...current, [asset.id]: asset }))
    const attrs = { asset_id: asset.id, ...(kind === 'image' ? { alt: imageAlt } : {}) }
    if (position !== undefined) editor.chain().focus().setNodeSelection(position).command(({ tr }) => { tr.setNodeMarkup(position, editor.schema.nodes[kind], attrs); return true }).run()
    else editor.chain().focus().insertContent({ type: kind, attrs }).run()
    setReplacePosition(undefined)
    setPickerKind(undefined)
  }
  const chooseAsset = (kind: RichTextMediaKind, asset: Asset) => {
    if (kind === 'image') {
      setPendingImage({ asset, kind, position: replacePosition })
      setAlt('')
      setPickerKind(undefined)
      return
    }
    insertMedia(kind, asset)
  }

  const formatButton = (active: boolean, name: string, content: React.ReactNode, command: () => boolean) => <Button size="small" disabled={disabled} type={active ? 'primary' : 'text'} aria-label={name} aria-pressed={active} onClick={() => run(command)}>{content}</Button>
  return <div className={`rich-text-editor${disabled ? ' is-disabled' : ''}`}>
    <div className="rich-text-toolbar" role="toolbar" aria-label={`${label} 格式工具栏`}>
      <Select aria-label="段落格式" size="small" disabled={disabled} value={state.heading ? `h${state.heading}` : 'paragraph'} onChange={(format) => run(() => format === 'paragraph' ? editor.chain().focus().setParagraph().run() : editor.chain().focus().setHeading({ level: Number(format.slice(1)) as 1 | 2 | 3 | 4 | 5 | 6 }).run())} options={[{ value: 'paragraph', label: '正文' }, ...Array.from({ length: 6 }, (_, index) => ({ value: `h${index + 1}`, label: `标题 ${index + 1}` }))]} />
      <Divider type="vertical" />
      <Space.Compact>{formatButton(state.bold, '粗体', <strong>B</strong>, () => editor.chain().focus().toggleBold().run())}{formatButton(state.italic, '斜体', <em>I</em>, () => editor.chain().focus().toggleItalic().run())}{formatButton(state.underline, '下划线', <u>U</u>, () => editor.chain().focus().toggleUnderline().run())}{formatButton(state.strike, '删除线', <s>S</s>, () => editor.chain().focus().toggleStrike().run())}{formatButton(state.code, '行内代码', <code>&lt;/&gt;</code>, () => editor.chain().focus().toggleCode().run())}</Space.Compact>
      <Divider type="vertical" />
      <Space.Compact>
        <Button size="small" disabled={disabled} type={state.bulletList ? 'primary' : 'text'} aria-pressed={state.bulletList} onClick={() => run(() => editor.chain().focus().toggleBulletList().run())}>项目列表</Button>
        <Button size="small" disabled={disabled} type={state.orderedList ? 'primary' : 'text'} aria-pressed={state.orderedList} onClick={() => run(() => editor.chain().focus().toggleOrderedList().run())}>编号列表</Button>
        <Button size="small" disabled={disabled} type={state.blockquote ? 'primary' : 'text'} aria-pressed={state.blockquote} onClick={() => run(() => editor.chain().focus().toggleBlockquote().run())}>引用</Button>
        <Button size="small" disabled={disabled} type={state.codeBlock ? 'primary' : 'text'} aria-pressed={state.codeBlock} onClick={() => run(() => editor.chain().focus().toggleCodeBlock().run())}>代码块</Button>
      </Space.Compact>
      {ASSETS_ENABLED && canSelectAssets ? <><Divider type="vertical" />{(['image', 'audio', 'video'] as const).map((kind) => <AssetPicker key={kind} multiple={false} value={null} onChange={() => undefined} disabled={disabled} canUpload={canUploadAssets} knownAssets={allAssets} kind={kind} triggerLabel={`插入${kind === 'image' ? '图片' : kind === 'audio' ? '音频' : '视频'}`} hideSelection open={pickerKind === kind} onOpenChange={(open) => { if (open) setPickerKind(kind); else { setPickerKind(undefined); setReplacePosition(undefined) } }} onAssetChosen={(asset) => chooseAsset(kind, asset)} />)}</> : null}
      <Divider type="vertical" />
      <Space.Compact><Button size="small" disabled={disabled || !state.canUndo} type="text" onClick={() => run(() => editor.chain().focus().undo().run())}>撤销</Button><Button size="small" disabled={disabled || !state.canRedo} type="text" onClick={() => run(() => editor.chain().focus().redo().run())}>重做</Button></Space.Compact>
    </div>
    <RichTextMediaContext value={environment}><EditorContent editor={editor} /></RichTextMediaContext>
    <Modal title="图片替代文本" open={Boolean(pendingImage)} okText={pendingImage?.position === undefined ? '插入图片' : '替换图片'} onCancel={() => { setPendingImage(undefined); setReplacePosition(undefined) }} onOk={() => { if (pendingImage) insertMedia('image', pendingImage.asset, alt, pendingImage.position); setPendingImage(undefined) }}><label className="rich-text-image-alt-dialog">替代文本<Input value={alt} maxLength={1000} onChange={(event) => setAlt(event.target.value)} placeholder="描述图片内容；装饰性图片可以留空" /></label></Modal>
  </div>
}
