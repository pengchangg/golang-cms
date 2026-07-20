import { Button, Progress, Select, Space, Table, Tag } from 'antd'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'

import { api } from '../api/client'
import type { Job, JobStatus, JobType } from '../api/types'
import { DataState, PageHeader, PendingApiNotice, useApiData } from '../components/Page'
import { jobStatusLabels, jobTypeLabels } from './jobLabels'

export default function JobsPage() {
  const [status, setStatus] = useState<JobStatus | undefined>()
  const [type, setType] = useState<JobType | undefined>()
  const [cursors, setCursors] = useState<Array<string | undefined>>([undefined])
  const jobs = useApiData(() => api.listJobs({ status, type, cursor: cursors.at(-1), limit: 20 }), [status, type, cursors.at(-1)])
  const hasActiveJobs = jobs.data?.items.some((job) => job.status === 'queued' || job.status === 'running') ?? false
  useEffect(() => {
    if (!hasActiveJobs) return
    const timer = window.setInterval(() => jobs.reload(true), 5_000)
    return () => window.clearInterval(timer)
    // reload 只递增请求序号；轮询生命周期由任务状态和筛选条件控制。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasActiveJobs, status, type, cursors.at(-1)])
  return <><PageHeader eyebrow="异步执行" title="传输任务" description="集中查看 CSV 导入导出的进度、行级错误和结果保留期限。" /><PendingApiNotice />
    <Space wrap className="filter-bar"><Select aria-label="任务类型" allowClear placeholder="全部类型" value={type} options={Object.entries(jobTypeLabels).map(([value, label]) => ({ value, label }))} onChange={(value) => { setType(value); setCursors([undefined]) }} /><Select aria-label="任务状态" allowClear placeholder="全部状态" value={status} options={Object.entries(jobStatusLabels).map(([value, label]) => ({ value, label }))} onChange={(value) => { setStatus(value); setCursors([undefined]) }} /></Space>
    <DataState loading={jobs.loading} error={jobs.error} empty={!jobs.data?.items.length} retry={jobs.reload}><Table<Job> rowKey="id" dataSource={jobs.data?.items} pagination={false} scroll={{ x: 760 }} columns={[
      { title: '任务', dataIndex: 'id', render: (id: string) => <Link to={`/jobs/${id}`}><code>{id}</code></Link> },
      { title: '类型', dataIndex: 'type', render: (value: JobType) => jobTypeLabels[value] },
      { title: '模型', dataIndex: 'model_id', render: (value: string) => <code>{value}</code> },
      { title: '状态', dataIndex: 'status', render: (value: JobStatus) => <Tag color={value === 'succeeded' ? 'green' : value === 'failed' ? 'red' : value === 'running' ? 'blue' : 'default'}>{jobStatusLabels[value]}</Tag> },
      { title: '进度', dataIndex: 'progress', render: (value: number) => <Progress percent={value} size="small" /> },
      { title: '创建时间', dataIndex: 'created_at', render: (value: string) => new Date(value).toLocaleString('zh-CN') },
    ]} /></DataState><Space className="pagination-actions"><Button disabled={cursors.length === 1} onClick={() => setCursors((values) => values.slice(0, -1))}>上一页</Button><Button disabled={!jobs.data?.next_cursor} onClick={() => jobs.data?.next_cursor && setCursors((values) => [...values, jobs.data!.next_cursor!])}>下一页</Button></Space>
  </>
}
