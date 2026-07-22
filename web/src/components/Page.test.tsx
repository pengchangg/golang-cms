import { act, cleanup, render, screen } from '@testing-library/react'
import { useEffect } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { useApiData } from './Page'

function PollingHarness({ load }: { load: () => Promise<string> }) {
  const result = useApiData(load)
  useEffect(() => {
    const timer = window.setInterval(() => result.reload(true), 1_000)
    return () => window.clearInterval(timer)
    // 测试固定模拟页面轮询生命周期。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return <div>{result.error ? '错误' : result.data ?? '加载中'}</div>
}

function MutationHarness({ load }: { load: () => Promise<string> }) {
  const result = useApiData(load)
  return <><div>{result.data ?? '加载中'}</div><button onClick={() => result.setData('乐观数据')}>更新</button><button onClick={() => result.reload(true)}>刷新</button><button onClick={result.invalidate}>失效</button></>
}

afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

describe('useApiData 背景刷新', () => {
  it('临时错误保留旧数据并在下一周期继续重试', async () => {
    vi.useFakeTimers()
    const load = vi.fn()
      .mockResolvedValueOnce('旧数据')
      .mockRejectedValueOnce(new Error('临时错误'))
      .mockResolvedValueOnce('新数据')
    render(<PollingHarness load={load} />)
    await act(async () => {})

    expect(screen.getByText('旧数据')).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(1_000))
    expect(screen.getByText('旧数据')).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(1_000))
    expect(screen.getByText('新数据')).toBeInTheDocument()
    expect(load).toHaveBeenCalledTimes(3)
  })

  it('旧 GET 不会覆盖 setData，reload 只接受最新请求', async () => {
    let resolveFirst!: (value: string) => void
    let resolveSecond!: (value: string) => void
    let resolveThird!: (value: string) => void
    const load = vi.fn()
      .mockReturnValueOnce(new Promise<string>((resolve) => { resolveFirst = resolve }))
      .mockReturnValueOnce(new Promise<string>((resolve) => { resolveSecond = resolve }))
      .mockReturnValueOnce(new Promise<string>((resolve) => { resolveThird = resolve }))
    render(<MutationHarness load={load} />)

    screen.getByText('更新').click()
    await act(async () => resolveFirst('过期数据'))
    expect(screen.getByText('乐观数据')).toBeInTheDocument()

    screen.getByText('刷新').click()
    await act(async () => {})
    screen.getByText('刷新').click()
    await act(async () => {})
    await act(async () => resolveSecond('较旧刷新'))
    expect(screen.getByText('乐观数据')).toBeInTheDocument()
    await act(async () => resolveThird('最新刷新'))
    expect(screen.getByText('最新刷新')).toBeInTheDocument()
  })

  it('invalidate 在依赖不变时也会重新请求并拒绝旧响应', async () => {
    let resolveFirst!: (value: string) => void
    let resolveSecond!: (value: string) => void
    const load = vi.fn()
      .mockReturnValueOnce(new Promise<string>((resolve) => { resolveFirst = resolve }))
      .mockReturnValueOnce(new Promise<string>((resolve) => { resolveSecond = resolve }))
    render(<MutationHarness load={load} />)
    screen.getByText('失效').click()
    await act(async () => {})
    expect(load).toHaveBeenCalledTimes(2)
    await act(async () => resolveFirst('过期数据'))
    expect(screen.getByText('加载中')).toBeInTheDocument()
    await act(async () => resolveSecond('新数据'))
    expect(screen.getByText('新数据')).toBeInTheDocument()
  })
})
