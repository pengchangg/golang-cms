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
})
