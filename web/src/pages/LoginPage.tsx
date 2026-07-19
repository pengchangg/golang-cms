import { Alert, Button, Divider, Form, Input, Typography } from 'antd'
import { useState } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'

import { api, oidcStartUrl, safeReturnTo } from '../api/client'
import { authStore, useAuthState } from '../auth/store'
import { RequestError } from '../components/RequestError'

const { Paragraph, Text, Title } = Typography

interface LoginValues {
  username: string
  password: string
}

export function LoginPage() {
  const auth = useAuthState()
  const location = useLocation()
  const navigate = useNavigate()
  const [error, setError] = useState<unknown>()
  const [submitting, setSubmitting] = useState(false)
  const returnTo = safeReturnTo(
    typeof location.state === 'object' &&
    location.state &&
    'returnTo' in location.state &&
    typeof location.state.returnTo === 'string'
      ? location.state.returnTo
      : undefined,
  ) ?? '/'
  const oidcFailed = Boolean(
    typeof location.state === 'object' &&
      location.state &&
      'oidcFailed' in location.state &&
      location.state.oidcFailed === true,
  )

  if (auth.status === 'authenticated') return <Navigate to={returnTo} replace />

  async function login(values: LoginValues) {
    setSubmitting(true)
    setError(undefined)
    try {
      const session = await api.localLogin(values.username, values.password)
      authStore.setSession(session)
      navigate(returnTo, { replace: true })
    } catch (nextError) {
      setError(nextError)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="login-page" id="main-content">
      <section className="login-intro" aria-labelledby="login-title">
        <Text className="eyebrow">企业内容中台</Text>
        <Title id="login-title">回到内容工作的现场</Title>
        <Paragraph>
          使用企业单点登录进入管理端。权限由组织统一配置，并在每次请求时重新确认。
        </Paragraph>
        <div className="login-index" aria-hidden="true">01</div>
      </section>

      <section className="login-actions" aria-label="登录方式">
        <div>
          <Title level={2}>登录</Title>
          <Paragraph type="secondary">优先使用企业身份登录</Paragraph>
        </div>
        {oidcFailed ? (
          <Alert type="error" showIcon title="企业身份登录未完成，请重新尝试或使用本地应急登录。" />
        ) : null}
        <Button type="primary" size="large" block href={oidcStartUrl(returnTo)}>
          使用企业 SSO 登录
        </Button>
        <Divider plain>应急访问</Divider>
        <Form<LoginValues> layout="vertical" requiredMark={false} onFinish={login}>
          <Form.Item
            label="管理员账号"
            name="username"
            rules={[{ required: true, message: '请输入管理员账号' }]}
          >
            <Input autoComplete="username" maxLength={128} />
          </Form.Item>
          <Form.Item
            label="密码"
            name="password"
            rules={[{ required: true, message: '请输入密码' }]}
          >
            <Input.Password autoComplete="current-password" maxLength={1024} />
          </Form.Item>
          {error ? <RequestError error={error} /> : null}
          <Button htmlType="submit" block loading={submitting}>
            本地应急登录
          </Button>
        </Form>
      </section>
    </main>
  )
}
