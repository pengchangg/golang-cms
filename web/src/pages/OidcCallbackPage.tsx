import { Result } from 'antd'
import { Link } from 'react-router-dom'

export function OidcCallbackPage() {
  return (
    <main className="callback-page" id="main-content" aria-live="polite">
      <Result
        status="error"
        title="企业身份登录未完成"
        subTitle="登录服务未能完成本次认证，请返回登录页重试。"
        extra={<Link to="/login" replace state={{ oidcFailed: true }}>返回登录</Link>}
      />
    </main>
  )
}
