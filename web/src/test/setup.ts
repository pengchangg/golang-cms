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

const emptyClientRects = () => [] as unknown as DOMRectList
const emptyClientRect = () => ({ x: 0, y: 0, width: 0, height: 0, top: 0, right: 0, bottom: 0, left: 0, toJSON: () => ({}) }) as DOMRect
Range.prototype.getBoundingClientRect = emptyClientRect
Range.prototype.getClientRects = emptyClientRects
Element.prototype.getClientRects = emptyClientRects
Element.prototype.getBoundingClientRect = emptyClientRect

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
