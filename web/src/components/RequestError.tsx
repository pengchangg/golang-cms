import { Alert, Typography } from 'antd'

import { ApiError } from '../api/client'

export function RequestError({ error }: { error: unknown }) {
  const apiError = error instanceof ApiError ? error : null
  return (
    <Alert
      type="error"
      showIcon
      title={apiError?.message ?? '暂时无法完成请求，请稍后重试'}
      description={
        apiError ? (
          <Typography.Text className="request-id">
            请求 ID：<code>{apiError.requestId}</code>
          </Typography.Text>
        ) : undefined
      }
    />
  )
}
