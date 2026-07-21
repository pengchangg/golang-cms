import { Alert, Button, Empty, Spin, Typography } from 'antd'
import { useEffect, useRef, useState, type ReactNode } from 'react'

import { RequestError } from './RequestError'

const { Paragraph, Text, Title } = Typography

export function PageHeader({ eyebrow, title, description, extra }: { eyebrow: string; title: string; description: string; extra?: ReactNode }) {
  return <header className="content-heading page-heading"><div><Text className="eyebrow">{eyebrow}</Text><Title>{title}</Title><Paragraph>{description}</Paragraph></div>{extra}</header>
}

// eslint-disable-next-line react-refresh/only-export-components
export function useApiData<T>(load: () => Promise<T>, dependencies: readonly unknown[] = []) {
  const [state, setState] = useState<{ data?: T; error?: unknown; loading: boolean }>({ loading: true })
  const dataRef = useRef<T | undefined>(undefined)
  const generationRef = useRef(0)
  const [attempt, setAttempt] = useState({ id: 0, background: false })
  useEffect(() => {
    const generation = ++generationRef.current
    load().then((data) => {
      if (generation !== generationRef.current) return
      dataRef.current = data
      setState({ data, loading: false })
    }, (error) => {
      if (generation !== generationRef.current) return
      if (attempt.background && dataRef.current !== undefined) {
        setState((current) => ({ ...current, loading: false }))
        return
      }
      dataRef.current = undefined
      setState({ error, loading: false })
    })
    return () => { generationRef.current += 1 }
    // 调用方通过依赖项明确控制重新请求。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...dependencies, attempt])
  return {
    ...state,
    reload: (background = false) => {
      generationRef.current += 1
      setAttempt((value) => ({ id: value.id + 1, background }))
    },
    setData: (data: T) => {
      generationRef.current += 1
      dataRef.current = data
      setState({ data, loading: false })
    },
  }
}

export function DataState({ loading, error, empty, retry, children }: { loading: boolean; error?: unknown; empty?: boolean; retry: () => void; children: ReactNode }) {
  if (loading) return <div className="page-state"><Spin size="large" /></div>
  if (error) return <div className="page-state"><RequestError error={error} /><Button onClick={retry}>重试</Button></div>
  if (empty) return <Empty description="暂无数据" />
  return children
}

export function PendingApiNotice() {
  return <Alert className="pending-notice" type="info" showIcon title="后端接口尚未聚合时，本页会如实显示请求错误" description="仅设置对应的 VITE_ENABLE_*_MOCK=true 开发变量时使用显式演示数据，生产环境不会伪造成功响应。" />
}
