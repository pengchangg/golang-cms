import { Alert, Button, Descriptions, Progress, Space, Table, Tag, Typography, message } from 'antd'
import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { ApiError, adminDownloadUrl, api } from '../api/client'
import type { Job, Principal, TransferError } from '../api/types'
import { hasSystemPermission } from '../auth/permissions'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'
import { jobStatusLabels, jobTypeLabels } from './jobLabels'

export default function JobDetailPage({ principal }: { principal: Principal }) {
  const { jobId = '' } = useParams()
  const [loadedAt] = useState(() => Date.now())
  const [cursors, setCursors] = useState<Array<string | undefined>>([undefined])
  const job = useApiData(() => api.getJob(jobId), [jobId])
  const isActive = job.data?.status === 'queued' || job.data?.status === 'running'
  useEffect(() => {
    if (!isActive) return
    const timer = window.setInterval(() => job.reload(true), 5_000)
    return () => window.clearInterval(timer)
    // reload 只递增请求序号；终态或卸载时清理定时器。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isActive, jobId])
  const showErrors = job.data?.type === 'csv_import' && job.data.status === 'failed'
  const errors = useApiData(() => showErrors ? api.listJobErrors(jobId, cursors.at(-1)) : Promise.resolve({ items: [], next_cursor: null, errors_truncated: false }), [jobId, showErrors, cursors.at(-1)])
  const canDownload = hasSystemPermission(principal, 'transfers.download')
  const expired = Boolean(job.data?.expires_at && new Date(job.data.expires_at).getTime() <= loadedAt)

  async function act(run: () => Promise<Job>, success: string) {
    try {
      job.setData(await run())
      message.success(success)
    } catch (error) {
      message.error(error instanceof ApiError ? error.message : '任务操作失败')
    }
  }

  return <><PageHeader eyebrow="通用任务状态" title="任务详情" description="状态来自持久任务记录，进度仅用于展示，不作为完成依据。" extra={<Link to="/jobs"><Button>返回任务列表</Button></Link>} /><PendingApiNotice />
    <DataState loading={job.loading} error={job.error} retry={job.reload}><section className="job-sheet">
      {job.data ? <><div className="job-state"><Tag>{jobStatusLabels[job.data.status]}</Tag><Progress percent={job.data.progress} /></div>
      <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }} items={[
        { key: 'id', label: '任务 ID', children: <code>{job.data.id}</code> }, { key: 'type', label: '任务类型', children: jobTypeLabels[job.data.type] },
        { key: 'model', label: '模型 ID', children: <code>{job.data.model_id}</code> }, { key: 'attempt', label: '执行次数', children: `${job.data.attempt} / ${job.data.max_attempts}` },
        { key: 'created', label: '创建时间', children: new Date(job.data.created_at).toLocaleString('zh-CN') }, { key: 'expires', label: '文件过期', children: job.data.expires_at ? new Date(job.data.expires_at).toLocaleString('zh-CN') : '尚未生成' },
      ]} />
       {job.data.status === 'failed' ? <Alert type="error" showIcon title={job.data.error_message ?? '任务失败'} description={job.data.error_code ? <code>{job.data.error_code}</code> : undefined} /> : null}
      {expired ? <Alert type="warning" showIcon title="任务文件已过期" description="错误报告或导出结果已超过保留期限，不能再次下载。" /> : null}
      <Space wrap className="job-actions">{['queued', 'running'].includes(job.data.status) ? <Button danger onClick={() => act(() => api.cancelJob(jobId), '已提交取消请求')}>取消任务</Button> : null}{job.data.status === 'failed' && job.data.attempt < job.data.max_attempts ? <Button onClick={() => act(() => api.retryJob(jobId), '任务已重新排队')}>重试任务</Button> : null}{job.data.status === 'succeeded' && job.data.type === 'csv_export' ? <Button type="primary" href={adminDownloadUrl(`/jobs/${encodeURIComponent(jobId)}/files/result`)} disabled={!canDownload || expired}>下载导出结果</Button> : null}{showErrors ? <Button href={adminDownloadUrl(`/jobs/${encodeURIComponent(jobId)}/files/errors`)} disabled={!canDownload || expired}>下载错误报告</Button> : null}</Space></> : null}
    </section></DataState>
    {showErrors ? <section className="job-errors"><Typography.Title level={2}>行级错误</Typography.Title>{errors.data?.errors_truncated ? <Alert type="warning" showIcon title="错误详情已截断" description="仅保存前 100,000 条，请下载报告后结合模型规则修正源文件。" /> : null}<DataState loading={errors.loading} error={errors.error} empty={!errors.data?.items.length} retry={errors.reload}><Table<TransferError> size="small" rowKey={(item) => `${item.row}-${item.field}-${item.code}`} dataSource={errors.data?.items} pagination={false} columns={[{ title: '行', dataIndex: 'row' }, { title: '字段', dataIndex: 'field', render: (value: string) => value || '文件' }, { title: '代码', dataIndex: 'code', render: (value: string) => <code>{value}</code> }, { title: '说明', dataIndex: 'message' }]} /></DataState><Space className="pagination-actions"><Button disabled={cursors.length === 1} onClick={() => setCursors((values) => values.slice(0, -1))}>上一页错误</Button><Button disabled={!errors.data?.next_cursor} onClick={() => errors.data?.next_cursor && setCursors((values) => [...values, errors.data!.next_cursor!])}>下一页错误</Button></Space></section> : null}
  </>
}
