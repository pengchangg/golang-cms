import { LogoutOutlined, MenuOutlined, UserOutlined } from '@ant-design/icons'
import { Avatar, Button, Drawer, Dropdown, Layout, Menu, Typography, message } from 'antd'
import { lazy, Suspense, useState, type ReactNode } from 'react'
import { Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'

import { api, apiErrorMessage } from '../api/client'
import type { ModelPermission } from '../api/types'
import { visibleNavigation } from '../auth/permissions'
import { authStore, useAuthState } from '../auth/store'
import { PermissionRoute } from '../components/PermissionRoute'
import { ASSETS_ENABLED, CONTENT_API_EXPLORER_ENABLED } from '../config'
import { apiKeyPagePermissions, navigation, rolePagePermissions } from './navigation'

const WorkspacePage = lazy(() => import('./WorkspacePage'))
const AccountPage = lazy(() => import('./AccountPage'))
const UsersPage = lazy(() => import('./UsersPage'))
const RolesPage = lazy(() => import('./RolesPage'))
const ModelsPage = lazy(() => import('./ModelsPage'))
const ModelDesignerPage = lazy(() => import('./ModelDesignerPage'))
const EntriesPage = lazy(() => import('./EntriesPage'))
const EntryEditorPage = lazy(() => import('./EntryEditorPage'))
const AuditPage = lazy(() => import('./AuditPage'))
const APIKeysPage = lazy(() => import('./APIKeysPage'))
const APIExplorerPage = (
  import.meta.env.VITE_CONTENT_API_EXPLORER_ENABLED === 'true' ||
  (import.meta.env.VITE_CONTENT_API_EXPLORER_ENABLED === undefined && import.meta.env.DEV)
) ? lazy(() => import('./APIExplorerPage')) : null
const AssetsPage = lazy(() => import('./AssetsPage'))
const { Header, Sider, Content } = Layout

export default function AppShell() {
  const auth = useAuthState()
  const location = useLocation()
  const navigate = useNavigate()
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [loggingOut, setLoggingOut] = useState(false)

  if (auth.status === 'loading') return <main className="loading-page" id="main-content" aria-label="正在加载会话" />
  if (auth.status === 'anonymous') {
    return <Navigate to="/login" replace state={{ returnTo: `${location.pathname}${location.search}${location.hash}` }} />
  }
  if (auth.status !== 'authenticated') return <main className="loading-page" id="main-content" aria-label="正在加载会话" />

  const { session } = auth
  const { principal } = session
  const modelByID = new Map(session.content_models.map((model) => [model.id, model]))
  const contentItems = principal.model_permissions
    .filter((grant) => grant.permissions.includes('content.view'))
    .flatMap((grant) => {
      const model = modelByID.get(grant.model_id)
      return model ? [{ key: `content-${grant.model_id}`, label: <span className="model-menu-label" title={`${model.display_name} · ${model.key}`}><strong>{model.display_name}</strong><small>{model.key}</small></span>, textLabel: model.display_name, path: `/content/${grant.model_id}` }] : []
    })
  const items = [...visibleNavigation(navigation, principal), ...contentItems]
  const selected = [...items].reverse().find((item) => item.path === '/' ? location.pathname === '/' : location.pathname.startsWith(item.path)) ?? items[0]

  async function logout() {
    const epoch = authStore.beginTransition()
    setLoggingOut(true)
    try {
      await api.logout()
      authStore.clear(epoch)
    } catch (error) {
      message.error(apiErrorMessage(error, '退出登录失败'))
    } finally {
      setLoggingOut(false)
    }
  }

  const menu = (
    <Menu
      selectedKeys={selected ? [selected.key] : []}
      items={items.map(({ key, label }) => ({ key, label }))}
      onClick={({ key }) => {
        const item = items.find((candidate) => candidate.key === key)
        if (item) navigate(item.path)
        setDrawerOpen(false)
      }}
    />
  )
  const systemRoute = (permission: Parameters<typeof PermissionRoute>[0]['system'], child: ReactNode) => (
    <PermissionRoute principal={principal} system={permission}>{child}</PermissionRoute>
  )
  const contentRoute = (permission: ModelPermission, child: ReactNode) => (
    <PermissionRoute principal={principal} model={{ id: location.pathname.split('/')[2] ?? '', permission }}>{child}</PermissionRoute>
  )

  return (
    <Layout className="app-shell">
      <a className="skip-link" href="#main-content">跳到主要内容</a>
      <Sider className="app-sider" width={240} breakpoint="lg" collapsedWidth={0} trigger={null}>
        <a className="brand" href="/" aria-label="内容管理系统首页"><span aria-hidden="true">内</span><strong>内容管理系统</strong></a>
        <nav aria-label="主导航">{menu}</nav>
        <Typography.Text className="shell-phase">管理端 · F3</Typography.Text>
      </Sider>
      <Drawer className="mobile-navigation" placement="left" title="内容管理系统" open={drawerOpen} onClose={() => setDrawerOpen(false)}>
        <nav aria-label="移动端主导航">{menu}</nav>
      </Drawer>
      <Layout>
        <Header className="app-header">
          <Button className="menu-trigger" type="text" icon={<MenuOutlined />} aria-label="打开导航" onClick={() => setDrawerOpen(true)} />
          <Typography.Text className="section-name">{selected?.textLabel ?? selected?.label ?? '内容管理系统'}</Typography.Text>
          <Dropdown menu={{ items: [{ key: 'logout', label: '退出登录', icon: <LogoutOutlined />, danger: true }], onClick: logout }}>
            <Button className="user-menu" type="text" loading={loggingOut}><Avatar size={30} icon={<UserOutlined />} /><span>{principal.display_name}</span></Button>
          </Dropdown>
        </Header>
        <Content className="app-content" id="main-content">
          <Suspense fallback={<div className="route-loading"><span /></div>}>
            <Routes>
              <Route index element={<WorkspacePage principal={principal} />} />
              <Route path="account" element={<AccountPage session={session} logout={logout} loggingOut={loggingOut} />} />
              <Route path="users" element={systemRoute('users.view', <UsersPage principal={principal} />)} />
              <Route path="roles" element={systemRoute(rolePagePermissions, <RolesPage principal={principal} />)} />
              <Route path="models" element={systemRoute('models.view', <ModelsPage principal={principal} />)} />
              <Route path="models/:modelId" element={systemRoute('models.view', <ModelDesignerPage principal={principal} />)} />
              <Route path="content/:modelId" element={contentRoute('content.view', <EntriesPage principal={principal} />)} />
              <Route path="content/:modelId/new" element={contentRoute('content.create', <EntryEditorPage principal={principal} />)} />
              <Route path="content/:modelId/:entryId" element={contentRoute('content.view', <EntryEditorPage principal={principal} />)} />
              <Route path="api-keys" element={systemRoute(apiKeyPagePermissions, <APIKeysPage principal={principal} />)} />
              <Route path="api-explorer" element={CONTENT_API_EXPLORER_ENABLED && APIExplorerPage ? systemRoute(apiKeyPagePermissions, <APIExplorerPage />) : <Navigate to="/" replace />} />
              <Route path="assets" element={ASSETS_ENABLED ? systemRoute('assets.view', <AssetsPage principal={principal} />) : <Navigate to="/" replace />} />
              <Route path="audit" element={systemRoute('audit.view', <AuditPage principal={principal} />)} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </Suspense>
        </Content>
      </Layout>
    </Layout>
  )
}
