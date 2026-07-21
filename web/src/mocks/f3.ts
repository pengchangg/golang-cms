import type { Asset } from '../api/types'

const now = '2026-07-19T08:00:00Z'
let assets: Asset[] = [
  { id: 'ast_cover_01', filename: '夏季刊封面.png', mime_type: 'image/png', size: 248320, sha256: 'a'.repeat(64), etag: 'mock-etag-1', status: 'available', created_by: 'usr_dev_preview', created_at: now, confirmed_at: now, archived_at: null },
  { id: 'ast_manual_02', filename: '产品手册.pdf', mime_type: 'application/pdf', size: 840120, sha256: 'b'.repeat(64), etag: 'mock-etag-2', status: 'available', created_by: 'usr_dev_preview', created_at: now, confirmed_at: now, archived_at: null },
]
function body(init?: RequestInit) { return JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown> }

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
    if (/^\/models\/[^/]+\/imports$/.test(path) && method === 'POST') return Response.json({ imported: 2 })
    if (/^\/models\/[^/]+\/exports\.csv$/.test(path) && method === 'GET') return new Response('\uFEFFtitle\n示例', { headers: { 'Content-Type': 'text/csv' } })
    return nativeFetch(input, init)
  }
}
