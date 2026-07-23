import { Editor } from '@tinymce/tinymce-react'
import { Input, Modal, message } from 'antd'
import { useEffect, useMemo, useRef, useState, type ChangeEvent } from 'react'
import type { Editor as TinyMCEEditor } from 'tinymce'

import 'tinymce/tinymce'
import 'tinymce/models/dom'
import 'tinymce/themes/silver'
import 'tinymce/icons/default'
import 'tinymce/plugins/lists'
import 'tinymce/plugins/link'
import 'tinymce/plugins/image'
import 'tinymce/plugins/table'
import 'tinymce/plugins/code'
import 'tinymce/skins/ui/oxide/skin.min.css'
import oxideContentCss from 'tinymce/skins/ui/oxide/content.min.css?url'

import { adminDownloadUrl, uploadAssetFile } from '../api/client'
import type { Asset, AssetKind, ReferencedAsset } from '../api/types'
import { ASSETS_ENABLED } from '../config'
import { AssetPicker } from './AssetPicker'
import { guessImageMime, hydrateRichTextHTML, isRichTextImageFile, normalizeRichTextHTML, richTextMediaHTML } from './richText'

const RICH_TEXT_IMAGE_FILE_TYPES = 'jpeg,jpg,png,gif,webp,avif'

interface PendingImage {
  asset: Asset
}

interface RichTextEditorProps {
  value: unknown
  onChange: (value: unknown) => void
  disabled: boolean
  label: string
  layout?: 'embedded' | 'fullscreen'
  labelledBy?: string
  describedBy?: string
  canSelectAssets?: boolean
  canUploadAssets?: boolean
  referencedAssets?: Record<string, ReferencedAsset>
  modelId?: string
  entryId?: string
}

export function RichTextEditor({
  value,
  onChange,
  disabled,
  label,
  layout = 'embedded',
  labelledBy,
  describedBy,
  canSelectAssets = false,
  canUploadAssets = false,
  referencedAssets = {},
  modelId,
  entryId,
}: RichTextEditorProps) {
  const editorRef = useRef<TinyMCEEditor | null>(null)
  const onChangeRef = useRef(onChange)
  const externalRef = useRef(normalizeRichTextHTML(value))
  const [localAssets, setLocalAssets] = useState<Record<string, Asset>>({})
  const [pickerKind, setPickerKind] = useState<AssetKind | undefined>(undefined)
  const [pendingImage, setPendingImage] = useState<PendingImage | undefined>(undefined)
  const [alt, setAlt] = useState('')
  const allAssets: Record<string, ReferencedAsset> = { ...referencedAssets, ...localAssets }

  useEffect(() => { onChangeRef.current = onChange }, [onChange])

  const mediaUrl = (assetID: string, action: 'preview' | 'download') => {
    if (!ASSETS_ENABLED) return undefined
    const encoded = encodeURIComponent(assetID)
    if (entryId && modelId && referencedAssets[assetID]) {
      return adminDownloadUrl(`/models/${encodeURIComponent(modelId)}/entries/${encodeURIComponent(entryId)}/assets/${encoded}/${action}`)
    }
    if (canSelectAssets) return adminDownloadUrl(`/assets/${encoded}/${action}`)
    return undefined
  }

  const hydratedValue = useMemo(
    () => hydrateRichTextHTML(normalizeRichTextHTML(value), (assetID) => mediaUrl(assetID, 'preview')),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- refresh when asset maps or value change
    [value, referencedAssets, localAssets, canSelectAssets, modelId, entryId],
  )

  useEffect(() => {
    const next = normalizeRichTextHTML(value)
    if (next === externalRef.current) return
    externalRef.current = next
    const editor = editorRef.current
    if (!editor || editor.isHidden()) return
    const current = normalizeRichTextHTML(editor.getContent())
    if (current !== next) {
      editor.setContent(hydrateRichTextHTML(next, (assetID) => mediaUrl(assetID, 'preview')), { format: 'html' })
    }
  }, [value, referencedAssets, localAssets, canSelectAssets, modelId, entryId])

  const canUploadAssetsRef = useRef(canUploadAssets)
  const mediaUrlRef = useRef(mediaUrl)
  useEffect(() => { canUploadAssetsRef.current = canUploadAssets }, [canUploadAssets])
  useEffect(() => { mediaUrlRef.current = mediaUrl })

  const emitChange = (editor: TinyMCEEditor) => {
    const next = normalizeRichTextHTML(editor.getContent())
    externalRef.current = next
    onChangeRef.current(next)
  }

  const insertMedia = (kind: AssetKind, asset: Asset, imageAlt = '') => {
    const editor = editorRef.current
    if (!editor) return
    setLocalAssets((current) => ({ ...current, [asset.id]: asset }))
    const html = richTextMediaHTML(kind, asset.id, {
      alt: imageAlt,
      previewUrl: mediaUrlRef.current(asset.id, 'preview') ?? adminDownloadUrl(`/assets/${encodeURIComponent(asset.id)}/preview`),
    })
    editor.insertContent(html)
    emitChange(editor)
    setPickerKind(undefined)
  }

  const uploadDroppedImages = async (files: File[]) => {
    if (!ASSETS_ENABLED || !canUploadAssetsRef.current) {
      message.warning('当前无法上传图片，请使用工具栏素材库插入')
      return
    }
    const images = files.filter(isRichTextImageFile)
    if (!images.length) {
      message.warning('仅支持拖入 JPEG、PNG、GIF、WebP、AVIF 图片')
      return
    }
    for (const file of images) {
      try {
        const asset = await uploadAssetFile(file)
        insertMedia('image', asset, '')
      } catch (error) {
        message.error(error instanceof Error ? error.message : `上传“${file.name}”失败`)
      }
    }
  }

  const chooseAsset = (kind: AssetKind, asset: Asset) => {
    if (kind === 'image') {
      setPendingImage({ asset })
      setAlt('')
      setPickerKind(undefined)
      return
    }
    insertMedia(kind, asset)
  }

  const init = useMemo(() => ({
    menubar: false,
    branding: false,
    promotion: false,
    statusbar: true,
    resize: false,
    skin: false,
    content_css: oxideContentCss,
    height: layout === 'fullscreen' ? '100%' : 420,
    plugins: 'lists link image table code',
    toolbar: ASSETS_ENABLED && canSelectAssets
      ? 'undo redo | blocks | bold italic underline strikethrough | alignleft aligncenter alignright | bullist numlist | link table | cmsImage cmsAudio cmsVideo | code'
      : 'undo redo | blocks | bold italic underline strikethrough | alignleft aligncenter alignright | bullist numlist | link table | code',
    block_formats: '段落=p; 标题 1=h1; 标题 2=h2; 标题 3=h3; 标题 4=h4; 标题 5=h5; 标题 6=h6; 引用=blockquote; 代码块=pre',
    object_resizing: 'img',
    resize_img_proportional: true,
    image_dimensions: true,
    images_file_types: RICH_TEXT_IMAGE_FILE_TYPES,
    block_unsupported_drop: false,
    convert_urls: false,
    relative_urls: false,
    automatic_uploads: canUploadAssets,
    images_reuse_filename: true,
    paste_data_images: canUploadAssets,
    content_style: `
      body { font-family: "IBM Plex Sans", "Noto Sans SC", sans-serif; font-size: 15px; line-height: 1.65; color: #1f1f1a; margin: 16px; }
      img { max-width: 100%; height: auto; cursor: default; }
      img[data-mce-selected] { outline: 2px solid #256b55; outline-offset: 2px; }
      video { max-width: 100%; height: auto; }
      audio { width: 100%; }
      blockquote { margin: 0; padding-left: 1em; border-left: 3px solid #c9c4b5; color: #5c574c; }
      pre { background: #f3f1ea; padding: 12px; overflow: auto; }
      table { border-collapse: collapse; width: 100%; }
      td, th { border: 1px solid #c9c4b5; padding: 6px 8px; }
    `,
    setup: (editor: TinyMCEEditor) => {
      editorRef.current = editor
      editor.on('keydown', (event) => {
        if (event.key !== 'Tab') return
        // 表格单元格内保留 TinyMCE 默认「跳下一格」，焦点仍在编辑器内
        if (editor.dom.getParent(editor.selection.getNode(), 'td,th,caption')) return

        event.preventDefault()
        event.stopPropagation()

        const inList = Boolean(editor.dom.getParent(editor.selection.getNode(), 'li,dt,dd'))
        if (inList) {
          editor.execCommand(event.shiftKey ? 'Outdent' : 'Indent')
          return
        }
        if (event.shiftKey) return
        // 普通段落：插入两个 em 空格，不依赖 style（服务端清洗会剥掉 padding-left）
        editor.insertContent('\u2003\u2003')
      })
      editor.on('ObjectResized', (event) => {
        const target = event.target
        if (!(target instanceof HTMLImageElement)) return
        const width = Math.round(event.width)
        const height = Math.round(event.height)
        if (width > 0) target.setAttribute('width', String(width))
        if (height > 0) target.setAttribute('height', String(height))
        target.style.width = ''
        target.style.height = ''
        emitChange(editor)
      })
      editor.on('drop', (event) => {
        const files = Array.from(event.dataTransfer?.files ?? [])
        if (!files.length) return
        event.preventDefault()
        void uploadDroppedImages(files)
      })
      editor.on('dragover', (event) => {
        if (event.dataTransfer?.types?.includes('Files')) event.preventDefault()
      })
      if (ASSETS_ENABLED && canSelectAssets) {
        editor.ui.registry.addButton('cmsImage', {
          text: '图片',
          tooltip: '从素材库插入图片',
          onAction: () => setPickerKind('image'),
        })
        editor.ui.registry.addButton('cmsAudio', {
          text: '音频',
          tooltip: '从素材库插入音频',
          onAction: () => setPickerKind('audio'),
        })
        editor.ui.registry.addButton('cmsVideo', {
          text: '视频',
          tooltip: '从素材库插入视频',
          onAction: () => setPickerKind('video'),
        })
      }
    },
    images_upload_handler: canUploadAssets
      ? async (blobInfo: { blob: () => Blob; filename: () => string }) => {
          const blob = blobInfo.blob()
          const name = blobInfo.filename() || 'paste.png'
          const type = blob.type && blob.type !== 'application/octet-stream' ? blob.type : guessImageMime(name)
          const file = new File([blob], name, { type })
          if (!isRichTextImageFile(file)) {
            message.warning('仅支持粘贴 JPEG、PNG、GIF、WebP、AVIF 图片')
            throw new Error('unsupported image type')
          }
          try {
            const asset = await uploadAssetFile(file)
            setLocalAssets((current) => ({ ...current, [asset.id]: asset }))
            const url = mediaUrlRef.current(asset.id, 'preview') ?? adminDownloadUrl(`/assets/${encodeURIComponent(asset.id)}/preview`)
            queueMicrotask(() => {
              const current = editorRef.current
              if (!current) return
              for (const img of Array.from(current.getBody().querySelectorAll('img'))) {
                if (img.getAttribute('src') === url || img.src.includes(asset.id)) {
                  img.setAttribute('data-asset-id', asset.id)
                  if (!img.hasAttribute('alt')) img.setAttribute('alt', '')
                }
              }
              emitChange(current)
            })
            return url
          } catch (error) {
            message.error(error instanceof Error ? error.message : '粘贴图片上传失败')
            throw error
          }
        }
      : undefined,
  }), [layout, canSelectAssets, canUploadAssets])

  return (
    <div className={`rich-text-editor${disabled ? ' is-disabled' : ''}${layout === 'fullscreen' ? ' is-fullscreen' : ''}`}>
      <div className="rich-text-editor-scroll">
        <Editor
          licenseKey="gpl"
          disabled={disabled}
          value={hydratedValue}
          onEditorChange={(_, editor) => emitChange(editor)}
          init={init}
          textareaName={label}
          tinymceScriptSrc={undefined}
        />
        {labelledBy || describedBy ? (
          <span className="visually-hidden" id={labelledBy} aria-describedby={describedBy}>{label} 富文本编辑器</span>
        ) : null}
      </div>
      {ASSETS_ENABLED && canSelectAssets ? (
        <>
          <AssetPicker
            multiple={false}
            value={null}
            onChange={() => undefined}
            disabled={disabled}
            canUpload={canUploadAssets}
            knownAssets={allAssets}
            kind="image"
            hideSelection
            hideTrigger
            open={pickerKind === 'image'}
            onOpenChange={(open) => setPickerKind(open ? 'image' : undefined)}
            onAssetChosen={(asset) => chooseAsset('image', asset)}
            triggerLabel="选择图片"
          />
          <AssetPicker
            multiple={false}
            value={null}
            onChange={() => undefined}
            disabled={disabled}
            canUpload={canUploadAssets}
            knownAssets={allAssets}
            kind="audio"
            hideSelection
            hideTrigger
            open={pickerKind === 'audio'}
            onOpenChange={(open) => setPickerKind(open ? 'audio' : undefined)}
            onAssetChosen={(asset) => chooseAsset('audio', asset)}
            triggerLabel="选择音频"
          />
          <AssetPicker
            multiple={false}
            value={null}
            onChange={() => undefined}
            disabled={disabled}
            canUpload={canUploadAssets}
            knownAssets={allAssets}
            kind="video"
            hideSelection
            hideTrigger
            open={pickerKind === 'video'}
            onOpenChange={(open) => setPickerKind(open ? 'video' : undefined)}
            onAssetChosen={(asset) => chooseAsset('video', asset)}
            triggerLabel="选择视频"
          />
        </>
      ) : null}
      <Modal
        title="图片替代文本"
        open={Boolean(pendingImage)}
        okText="插入图片"
        onCancel={() => setPendingImage(undefined)}
        onOk={() => {
          if (pendingImage) insertMedia('image', pendingImage.asset, alt)
          setPendingImage(undefined)
        }}
      >
        <label className="rich-text-image-alt-dialog">
          替代文本
          <Input value={alt} maxLength={1000} onChange={(event: ChangeEvent<HTMLInputElement>) => setAlt(event.target.value)} placeholder="描述图片内容；装饰性图片可以留空" />
        </label>
      </Modal>
    </div>
  )
}
