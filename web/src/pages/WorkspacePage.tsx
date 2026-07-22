import { ApiOutlined, ArrowRightOutlined, DatabaseOutlined, FileImageOutlined, KeyOutlined, SafetyCertificateOutlined, TeamOutlined } from '@ant-design/icons'
import { Typography } from 'antd'
import { Link } from 'react-router-dom'

import type { Principal } from '../api/types'
import { visibleNavigation } from '../auth/permissions'
import { PageHeader } from '../components/Page'
import { workspaceLinks } from './navigation'

export default function WorkspacePage({ principal }: { principal: Principal }) {
  const icons = { users: <TeamOutlined />, roles: <SafetyCertificateOutlined />, models: <DatabaseOutlined />, assets: <FileImageOutlined />, 'api-keys': <KeyOutlined />, 'api-explorer': <ApiOutlined /> }
  const links = visibleNavigation(workspaceLinks, principal)
  return <><PageHeader eyebrow="工作台" title={`早上好，${principal.display_name}`} description="从模型结构到草稿内容，在同一处继续今天的管理工作。" /><section className="workspace-grid">{links.map((item, index) => <Link className="workspace-link" to={item.path} key={item.key}><span className="workspace-index">0{index + 1}</span><span className="workspace-icon">{icons[item.key as keyof typeof icons]}</span><Typography.Title level={2}>{item.label}</Typography.Title><Typography.Text type="secondary">进入{item.label}工作区</Typography.Text><ArrowRightOutlined /></Link>)}</section></>
}
