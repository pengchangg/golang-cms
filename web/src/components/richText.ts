const ASSET_ATTR = 'data-asset-id'

const RICH_TEXT_IMAGE_MIME = new Set(['image/jpeg', 'image/png', 'image/gif', 'image/webp', 'image/avif'])
const RICH_TEXT_IMAGE_EXT = /\.(jpe?g|png|gif|webp|avif)$/i

/** Match CMS rich-text image whitelist; fall back to extension when MIME is empty/octet-stream. */
export function isRichTextImageFile(file: File) {
  if (RICH_TEXT_IMAGE_MIME.has(file.type)) return true
  if ((!file.type || file.type === 'application/octet-stream') && RICH_TEXT_IMAGE_EXT.test(file.name)) return true
  return false
}

export function guessImageMime(filename: string) {
  const lower = filename.toLowerCase()
  if (lower.endsWith('.png')) return 'image/png'
  if (lower.endsWith('.gif')) return 'image/gif'
  if (lower.endsWith('.webp')) return 'image/webp'
  if (lower.endsWith('.avif')) return 'image/avif'
  if (lower.endsWith('.jpg') || lower.endsWith('.jpeg')) return 'image/jpeg'
  return 'image/png'
}

function dimensionFromStyle(value: string): string | undefined {
  const match = /^(\d+(?:\.\d+)?)px$/i.exec(value.trim())
  if (!match) return undefined
  const number = Math.round(Number(match[1]))
  if (!Number.isFinite(number) || number < 1 || number > 99999) return undefined
  return String(number)
}

/** Strip ephemeral preview URLs before persisting HTML. */
export function normalizeRichTextHTML(value: unknown): string {
  if (typeof value !== 'string') return ''
  if (typeof document === 'undefined') return value.trim()
  const template = document.createElement('template')
  template.innerHTML = value
  for (const node of template.content.querySelectorAll('img, audio, video')) {
    const assetID = node.getAttribute(ASSET_ATTR)?.trim() ?? ''
    if (!assetID) {
      node.remove()
      continue
    }
    node.setAttribute(ASSET_ATTR, assetID)
    node.removeAttribute('src')
    node.removeAttribute('srcset')
    if (node instanceof HTMLImageElement) {
      if (!node.hasAttribute('alt')) node.setAttribute('alt', '')
      const styleWidth = dimensionFromStyle(node.style.width)
      const styleHeight = dimensionFromStyle(node.style.height)
      if (styleWidth) node.setAttribute('width', styleWidth)
      if (styleHeight) node.setAttribute('height', styleHeight)
      node.removeAttribute('style')
      for (const name of Array.from(node.getAttributeNames())) {
        if (!['data-asset-id', 'alt', 'width', 'height'].includes(name)) node.removeAttribute(name)
      }
    } else {
      node.setAttribute('controls', '')
      node.removeAttribute('style')
      for (const name of Array.from(node.getAttributeNames())) {
        if (!['data-asset-id', 'controls'].includes(name)) node.removeAttribute(name)
      }
    }
  }
  for (const node of template.content.querySelectorAll('[onclick], [onload], [onerror], script, style, iframe, object, embed')) {
    node.remove()
  }
  return template.innerHTML.trim()
}

/** Inject preview URLs for the editor session from known assets. */
export function hydrateRichTextHTML(value: unknown, previewUrl: (assetID: string) => string | undefined): string {
  const html = typeof value === 'string' ? value : ''
  if (!html || typeof document === 'undefined') return html
  const template = document.createElement('template')
  template.innerHTML = html
  for (const node of template.content.querySelectorAll('img, audio, video')) {
    const assetID = node.getAttribute(ASSET_ATTR)?.trim()
    if (!assetID) continue
    const url = previewUrl(assetID)
    if (url) node.setAttribute('src', url)
  }
  return template.innerHTML
}

export function richTextMediaHTML(kind: 'image' | 'audio' | 'video', assetID: string, options: { alt?: string; width?: number; previewUrl?: string } = {}): string {
  if (kind === 'image') {
    const alt = options.alt ?? ''
    const width = options.width ? ` width="${options.width}"` : ''
    const src = options.previewUrl ? ` src="${escapeAttr(options.previewUrl)}"` : ''
    return `<img data-asset-id="${escapeAttr(assetID)}" alt="${escapeAttr(alt)}"${width}${src}>`
  }
  const src = options.previewUrl ? ` src="${escapeAttr(options.previewUrl)}"` : ''
  return `<${kind} data-asset-id="${escapeAttr(assetID)}" controls${src}></${kind}>`
}

export function richTextPlainText(value: unknown, maxLength = 240): string {
  if (typeof value !== 'string' || !value.trim()) return ''
  if (typeof document === 'undefined') {
    const text = value.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ').trim()
    return text.length <= maxLength ? text : `${text.slice(0, maxLength).trimEnd()}…`
  }
  const template = document.createElement('template')
  template.innerHTML = value
  const parts: string[] = []
  const walk = (node: Node) => {
    if (node.nodeType === Node.TEXT_NODE) {
      const text = node.textContent ?? ''
      if (text) parts.push(text)
      return
    }
    if (!(node instanceof HTMLElement)) return
    const tag = node.tagName.toLowerCase()
    if (tag === 'img') parts.push('[图片]\n')
    else if (tag === 'audio') parts.push('[音频]\n')
    else if (tag === 'video') parts.push('[视频]\n')
    else if (tag === 'br') parts.push('\n')
    else {
      for (const child of Array.from(node.childNodes)) walk(child)
      if (['p', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'li', 'tr'].includes(tag)) parts.push('\n')
    }
  }
  for (const child of Array.from(template.content.childNodes)) walk(child)
  const text = parts.join('').replace(/\n{3,}/g, '\n\n').trim()
  if (text.length <= maxLength) return text
  return `${text.slice(0, maxLength).trimEnd()}…`
}

function escapeAttr(value: string) {
  return value.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}
