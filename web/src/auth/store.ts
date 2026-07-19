import { useSyncExternalStore } from 'react'

import type { SessionResponse } from '../api/types'

export type AuthState =
  | { status: 'loading'; session: null }
  | { status: 'anonymous'; session: null }
  | { status: 'error'; session: null; error: unknown }
  | { status: 'authenticated'; session: SessionResponse }

let state: AuthState = { status: 'loading', session: null }
const listeners = new Set<() => void>()

function emit() {
  listeners.forEach((listener) => listener())
}

export const authStore = {
  getSnapshot: () => state,
  subscribe(listener: () => void) {
    listeners.add(listener)
    return () => listeners.delete(listener)
  },
  setSession(session: SessionResponse) {
    state = { status: 'authenticated', session }
    emit()
  },
  clear() {
    state = { status: 'anonymous', session: null }
    emit()
  },
  setError(error: unknown) {
    state = { status: 'error', session: null, error }
    emit()
  },
  reset() {
    state = { status: 'loading', session: null }
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
