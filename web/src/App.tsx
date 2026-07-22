import { ConfigProvider } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import { lazy, Suspense, useState } from 'react'
import { createBrowserRouter, Navigate, Route, RouterProvider, Routes } from 'react-router-dom'

import { AuthProvider } from './auth/AuthProvider'
import './App.css'

const AppShell = lazy(() => import('./pages/AppShell'))
const LoginPage = lazy(() => import('./pages/LoginPage').then((module) => ({ default: module.LoginPage })))

function createRouter() {
  return createBrowserRouter([
    {
      path: '*',
      element: <AuthProvider><Suspense fallback={<main className="loading-page" aria-label="正在加载页面" />}><Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/*" element={<AppShell />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes></Suspense></AuthProvider>,
    },
  ])
}

function App() {
  const [router] = useState(createRouter)
  return (
    <ConfigProvider locale={zhCN}
      theme={{
        token: {
          colorPrimary: '#256b55',
          colorText: '#20241f',
          colorTextSecondary: '#686d65',
          fontFamily:
            '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Noto Sans SC", sans-serif',
          borderRadius: 8,
        },
      }}
    >
      <RouterProvider router={router} />
    </ConfigProvider>
  )
}

export default App
