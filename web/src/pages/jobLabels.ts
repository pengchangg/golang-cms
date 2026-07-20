import type { JobStatus, JobType } from '../api/types'

export const jobStatusLabels: Record<JobStatus, string> = { queued: '排队中', running: '执行中', succeeded: '已完成', failed: '失败', canceled: '已取消' }
export const jobTypeLabels: Record<JobType, string> = { csv_import: 'CSV 导入', csv_export: 'CSV 导出' }
