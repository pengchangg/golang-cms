import type { Asset, Job, TransferError } from '../api/types'

const now = '2026-07-19T08:00:00Z'
let assets: Asset[] = [
  { id: 'ast_cover_01', filename: '夏季刊封面.png', mime_type: 'image/png', size: 248320, sha256: 'a'.repeat(64), etag: 'mock-etag-1', status: 'available', created_by: 'usr_dev_preview', created_at: now, confirmed_at: now, archived_at: null },
  { id: 'ast_manual_02', filename: '产品手册.pdf', mime_type: 'application/pdf', size: 840120, sha256: 'b'.repeat(64), etag: 'mock-etag-2', status: 'available', created_by: 'usr_dev_preview', created_at: now, confirmed_at: now, archived_at: null },
]
let jobs: Job[] = [
  { id: 'job_import_failed', type: 'csv_import', status: 'failed', model_id: 'mdl_articles', progress: 100, attempt: 1, max_attempts: 3, cancel_requested_at: null, error_code: 'validation_failed', error_message: '2 行内容未通过校验', created_by: 'usr_dev_preview', created_at: now, started_at: now, finished_at: now, expires_at: '2026-07-26T08:00:00Z' },
  { id: 'job_export_done', type: 'csv_export', status: 'succeeded', model_id: 'mdl_articles', progress: 100, attempt: 1, max_attempts: 3, cancel_requested_at: null, error_code: null, error_message: null, created_by: 'usr_dev_preview', created_at: now, started_at: now, finished_at: now, expires_at: '2026-07-26T08:00:00Z' },
]
const errors: TransferError[] = [{ row: 2, field: 'title', code: 'validation_failed', message: '标题不能为空' }, { row: 3, field: 'cover', code: 'asset_not_available', message: '素材不可用' }]

function body(init?: RequestInit) { return JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown> }
function job(type: Job['type'], modelId: string): Job {
  return { id: `job_${crypto.randomUUID().slice(0, 8)}`, type, status: 'queued', model_id: modelId, progress: 0, attempt: 0, max_attempts: 3, cancel_requested_at: null, error_code: null, error_message: null, created_by: 'usr_dev_preview', created_at: now, started_at: null, finished_at: null, expires_at: null }
}

export function enableF3Mock() {
  const nativeFetch = window.fetch.bind(window)
  window.fetch = async (input, init) => {
    const raw = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const url = new URL(raw, window.location.origin)
    const method = (init?.method ?? 'GET').toUpperCase()
    if (url.hostname === 'mock-s3.local' && method === 'PUT') return new Response(null, { status: 200 })
    if (!url.pathname.startsWith('/api/admin/v1/')) return nativeFetch(input, init)
    const path = url.pathname.slice('/api/admin/v1'.length)
    if (path === '/assets' && method === 'GET') {
      const status = url.searchParams.get('status'); const mime = url.searchParams.get('mime_type')
      return Response.json({ items: assets.filter((asset) => (!status || asset.status === status) && (!mime || asset.mime_type === mime)), next_cursor: null })
    }
    if (path === '/assets/uploads' && method === 'POST') {
      const request = body(init); const asset: Asset = { id: `ast_${crypto.randomUUID().slice(0, 8)}`, filename: String(request.filename), mime_type: String(request.mime_type), size: Number(request.size), sha256: String(request.sha256), etag: null, status: 'quarantined', created_by: 'usr_dev_preview', created_at: now, confirmed_at: null, archived_at: null }
      assets = [asset, ...assets]
      return Response.json({ asset, upload: { method: 'PUT', url: `https://mock-s3.local/${asset.id}`, headers: { 'Content-Type': asset.mime_type, 'If-None-Match': '*', 'x-amz-meta-sha256': asset.sha256 }, expires_at: '2026-07-19T09:00:00Z' } }, { status: 201 })
    }
    const assetMatch = path.match(/^\/assets\/([^/]+)(\/confirm)?$/)
    if (assetMatch?.[2] && method === 'POST') {
      const asset = assets.find((item) => item.id === assetMatch[1])!; const confirmed = { ...asset, status: 'available' as const, etag: 'mock-upload-etag', confirmed_at: now }
      assets = assets.map((item) => item.id === asset.id ? confirmed : item); return Response.json(confirmed)
    }
    if (assetMatch && method === 'DELETE') { assets = assets.map((item) => item.id === assetMatch[1] ? { ...item, status: 'archived', archived_at: now } : item); return new Response(null, { status: 204 }) }
    const importUpload = path.match(/^\/models\/([^/]+)\/imports\/uploads$/)
    if (importUpload && method === 'POST') return Response.json({ upload_id: `upl_${crypto.randomUUID().slice(0, 8)}`, method: 'PUT', url: 'https://mock-s3.local/import.csv', headers: { 'Content-Type': 'text/csv' }, expires_at: '2026-07-19T09:00:00Z' }, { status: 201 })
    const transfer = path.match(/^\/models\/([^/]+)\/(imports|exports)$/)
    if (transfer && method === 'POST') { const created = job(transfer[2] === 'imports' ? 'csv_import' : 'csv_export', transfer[1]); jobs = [created, ...jobs]; return Response.json(created, { status: 201 }) }
    if (path === '/jobs' && method === 'GET') { const status = url.searchParams.get('status'); const type = url.searchParams.get('type'); return Response.json({ items: jobs.filter((item) => (!status || item.status === status) && (!type || item.type === type)), next_cursor: null }) }
    const jobMatch = path.match(/^\/jobs\/([^/]+)(?:\/(cancel|retry|errors))?$/)
    if (jobMatch && method === 'GET' && jobMatch[2] === 'errors') return Response.json({ items: errors, next_cursor: null, errors_truncated: false })
    if (jobMatch && method === 'GET') return Response.json(jobs.find((item) => item.id === jobMatch[1]))
    if (jobMatch && method === 'POST') { const current = jobs.find((item) => item.id === jobMatch[1])!; const updated: Job = jobMatch[2] === 'retry' ? { ...current, status: 'queued', error_code: null, error_message: null, progress: 0 } : { ...current, status: 'canceled', finished_at: now }; jobs = jobs.map((item) => item.id === updated.id ? updated : item); return Response.json(updated) }
    return nativeFetch(input, init)
  }
}
