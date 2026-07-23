import '@testing-library/jest-dom/vitest'
import { configure } from '@testing-library/dom'
import { afterAll } from 'vitest'

configure({ asyncUtilTimeout: 5000 })

const getComputedStyle = window.getComputedStyle.bind(window)
window.getComputedStyle = (element: Element) => getComputedStyle(element)

class ResizeObserverMock {
  observe() {}
  unobserve() {}
  disconnect() {}
}

globalThis.ResizeObserver = ResizeObserverMock

afterAll(async () => {
  // Ant Design 的 portal 关闭后会通过 React scheduler 提交最后一轮更新。
  const immediate = (globalThis as typeof globalThis & { setImmediate: (callback: () => void) => unknown }).setImmediate
  await new Promise<void>((resolve) => immediate(resolve))
  await new Promise<void>((resolve) => immediate(resolve))
})

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  }),
})
