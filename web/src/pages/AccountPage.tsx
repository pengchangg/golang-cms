import { LogoutOutlined, UserOutlined } from '@ant-design/icons'
import { Avatar, Button, Tag, Typography } from 'antd'
import type { SessionResponse } from '../api/types'
import { PageHeader } from '../components/Page'

const { Text, Title } = Typography
const authMethodLabels = { sms: '手机号短信', local: '本地应急' } as const
export default function AccountPage({ session, logout, loggingOut }: { session: SessionResponse; logout: () => void; loggingOut: boolean }) {
  const { principal } = session
  return <><PageHeader eyebrow="身份与访问" title="当前用户" description="这里只显示当前认证上下文，不会在浏览器中持久保存权限或令牌。" /><section className="identity-sheet"><div className="identity-primary"><Avatar size={64} icon={<UserOutlined />} /><div><Title level={2}>{principal.display_name}</Title><Text>{principal.email ?? (principal.auth_method === 'sms' ? '手机号账户' : '本地应急账户')}</Text></div></div><dl className="identity-details"><div><dt>身份方式</dt><dd><Tag>{authMethodLabels[principal.auth_method]}</Tag></dd></div><div><dt>用户标识</dt><dd><code>{principal.user_id}</code></dd></div><div><dt>空闲过期</dt><dd>{new Date(session.idle_expires_at).toLocaleString('zh-CN')}</dd></div><div><dt>绝对过期</dt><dd>{new Date(session.expires_at).toLocaleString('zh-CN')}</dd></div></dl><Button danger icon={<LogoutOutlined />} loading={loggingOut} onClick={logout}>退出当前会话</Button></section></>
}
