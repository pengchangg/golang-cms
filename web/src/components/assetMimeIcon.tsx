import {
  AudioOutlined, CodeOutlined, FileExcelOutlined, FilePdfOutlined, FilePptOutlined,
  FileTextOutlined, FileUnknownOutlined, FileWordOutlined, FileZipOutlined,
  PictureOutlined, TableOutlined, VideoCameraOutlined,
} from '@ant-design/icons'

function extension(filename: string) {
  return filename.toLowerCase().split('.').pop() ?? ''
}

function assetMimeCategory(mimeType: string, filename: string) {
  const mime = mimeType.toLowerCase()
  const ext = extension(filename)
  if (mime.startsWith('image/')) return '图片'
  if (mime === 'application/pdf' || ext === 'pdf') return 'PDF'
  if (mime.startsWith('video/')) return '视频'
  if (mime.startsWith('audio/')) return '音频'
  if (mime === 'text/csv' || ext === 'csv') return 'CSV'
  if (mime === 'application/json' || mime.endsWith('+json') || ext === 'json') return 'JSON'
  if (mime.includes('word') || mime.includes('wordprocessingml') || ['doc', 'docx'].includes(ext)) return 'Word'
  if (mime.includes('excel') || mime.includes('spreadsheetml') || ['xls', 'xlsx'].includes(ext)) return 'Excel'
  if (mime.includes('powerpoint') || mime.includes('presentationml') || ['ppt', 'pptx'].includes(ext)) return 'PPT'
  if (mime.includes('zip') || mime.includes('compressed') || mime.includes('archive') || ['zip', 'rar', '7z', 'tar', 'gz'].includes(ext)) return '压缩包'
  if (mime.startsWith('text/')) return '文本'
  return '未知文件'
}

export function AssetMimeIcon({ mimeType, filename }: { mimeType: string; filename: string }) {
  const category = assetMimeCategory(mimeType, filename)
  const icons = {
    图片: PictureOutlined, PDF: FilePdfOutlined, 视频: VideoCameraOutlined, 音频: AudioOutlined,
    文本: FileTextOutlined, CSV: TableOutlined, JSON: CodeOutlined, Word: FileWordOutlined,
    Excel: FileExcelOutlined, PPT: FilePptOutlined, 压缩包: FileZipOutlined, 未知文件: FileUnknownOutlined,
  }
  const Icon = icons[category]
  return <Icon aria-label={`${category}文件`} />
}
