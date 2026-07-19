import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import 'antd/dist/reset.css'

import App from './App'

if (import.meta.env.DEV && import.meta.env.VITE_ENABLE_AUTH_MOCK === 'true') {
  await import('./mocks/auth').then(({ enableAuthMock }) => enableAuthMock())
}
if (import.meta.env.DEV && import.meta.env.VITE_ENABLE_P1_MOCK === 'true') {
  await import('./mocks/p1').then(({ enableP1Mock }) => enableP1Mock())
}
if (import.meta.env.DEV && import.meta.env.VITE_ENABLE_F2_MOCK === 'true') {
  await import('./mocks/f2').then(({ enableF2Mock }) => enableF2Mock())
}

const root = document.getElementById('root')

if (!root) {
  throw new Error('缺少 React 挂载节点')
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
