import { Button, Result } from 'antd'
import { useEffect, useState, type PropsWithChildren } from 'react'

import { ApiError, api } from '../api/client'
import { RequestError } from '../components/RequestError'
import { authStore, useAuthState } from './store'

export function AuthProvider({ children }: PropsWithChildren) {
  const auth = useAuthState()
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let active = true
    const epoch = authStore.getEpoch()

    api.getSession().then(
      (session) => {
        if (active) authStore.setSession(session, epoch)
      },
      (error: unknown) => {
        if (!active) return
        if (error instanceof ApiError && error.status === 401 && error.code === 'session_invalid') return
        authStore.setError(error, epoch)
      },
    )

    return () => {
      active = false
    }
  }, [attempt])

  if (auth.status === 'error') {
    return (
      <main className="callback-page" id="main-content">
        <Result
          status="warning"
          title="暂时无法确认登录状态"
          subTitle={<RequestError error={auth.error} />}
          extra={
            <Button
              type="primary"
              onClick={() => {
                authStore.reset()
                setAttempt((value) => value + 1)
              }}
            >
              重试
            </Button>
          }
        />
      </main>
    )
  }

  return children
}
