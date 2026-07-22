import { useSyncExternalStore } from 'react'

import type { SessionResponse } from '../api/types'

export type AuthState =
  | { status: 'loading'; session: null }
  | { status: 'anonymous'; session: null }
  | { status: 'error'; session: null; error: unknown }
  | { status: 'authenticated'; session: SessionResponse }

let state: AuthState = { status: 'loading', session: null }
let epoch = 0
const listeners = new Set<() => void>()

function emit() {
  listeners.forEach((listener) => listener())
}

export const authStore = {
  getSnapshot: () => state,
  getEpoch: () => epoch,
  beginTransition() {
    epoch += 1
    return epoch
  },
  subscribe(listener: () => void) {
    listeners.add(listener)
    return () => listeners.delete(listener)
  },
  setSession(session: SessionResponse, expectedEpoch?: number) {
    if (expectedEpoch !== undefined && expectedEpoch !== epoch) return false
    state = { status: 'authenticated', session }
    epoch += 1
    emit()
    return true
  },
  clear(expectedEpoch?: number) {
    if (expectedEpoch !== undefined && expectedEpoch !== epoch) return false
    state = { status: 'anonymous', session: null }
    epoch += 1
    emit()
    return true
  },
  setError(error: unknown, expectedEpoch?: number) {
    if (expectedEpoch !== undefined && expectedEpoch !== epoch) return false
    state = { status: 'error', session: null, error }
    epoch += 1
    emit()
    return true
  },
  reset() {
    state = { status: 'loading', session: null }
    epoch += 1
    emit()
  },
}

export function useAuthState() {
  return useSyncExternalStore(
    authStore.subscribe,
    authStore.getSnapshot,
    authStore.getSnapshot,
  )
}
