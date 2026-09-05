import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'

const api = vi.hoisted(() => ({ login: vi.fn(), login2FA: vi.fn(), register: vi.fn(), logout: vi.fn(), getCurrentUser: vi.fn(), refreshToken: vi.fn() }))
const passkeyLogin = vi.hoisted(() => vi.fn())
vi.mock('@/api', () => ({ authAPI: api, isTotp2FARequired: () => false, passkeyAPI: { login: passkeyLogin } }))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (value: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

const alice = { id: 1, username: 'alice', email: 'alice@example.com', role: 'admin', balance: 10 }
const bob = { id: 2, username: 'bob', email: 'bob@example.com', role: 'user', balance: 20 }
function auth(user: typeof alice) {
  return { user, access_token: `access-${user.id}`, refresh_token: `refresh-${user.id}`, expires_in: 3600 }
}
async function login(store: ReturnType<typeof useAuthStore>, user: typeof alice) {
  api.login.mockResolvedValueOnce(auth(user))
  await store.login({ email: user.email, password: 'example-password' })
}

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
  vi.useFakeTimers()
  vi.resetAllMocks()
  api.logout.mockResolvedValue(undefined)
})
afterEach(() => { vi.clearAllTimers(); vi.useRealTimers() })

describe('auth session ownership', () => {
  it.each(['success', '401'] as const)('ignores an old user refresh %s after changing accounts', async (outcome) => {
    const store = useAuthStore()
    await login(store, alice)
    const request = deferred<{ data: typeof alice }>()
    api.getCurrentUser.mockReturnValueOnce(request.promise)
    const pending = store.refreshUser().catch(error => error)
    await store.logout()
    await login(store, bob)
    if (outcome === 'success') request.resolve({ data: alice })
    else request.reject({ status: 401, code: 'AUTH_SESSION_CHANGED' })
    await pending
    expect(store.token).toBe('access-2')
    expect(store.user?.id).toBe(2)
    expect(JSON.parse(localStorage.getItem('auth_user')!).id).toBe(2)
  })

  it('does not restore user data after logout', async () => {
    const store = useAuthStore()
    await login(store, alice)
    const request = deferred<{ data: typeof alice }>()
    api.getCurrentUser.mockReturnValueOnce(request.promise)
    const pending = store.refreshUser().catch(error => error)
    await store.logout()
    request.resolve({ data: alice })
    await pending
    expect(store.user).toBeNull()
    expect(localStorage.getItem('auth_user')).toBeNull()
  })

  it('retains a user refresh across normal access and refresh token rotation', async () => {
    const store = useAuthStore()
    await login(store, alice)
    const request = deferred<{ data: typeof alice }>()
    api.getCurrentUser.mockReturnValueOnce(request.promise)
    const pending = store.refreshUser()
    localStorage.setItem('auth_token', 'rotated-access')
    localStorage.setItem('refresh_token', 'rotated-refresh')
    request.resolve({ data: { ...alice, balance: 99 } })
    await pending
    expect(store.user?.balance).toBe(99)
  })

  it('does not erase a replacement session when an old logout finishes', async () => {
    const store = useAuthStore()
    await login(store, alice)
    const request = deferred<void>()
    api.logout.mockReturnValueOnce(request.promise)
    const pending = store.logout()
    await login(store, bob)
    request.resolve()
    await pending
    expect(store.token).toBe('access-2')
    expect(store.user?.id).toBe(2)
  })

  it.each(['login', 'register', 'login2FA', 'passkey'] as const)('discards an obsolete %s completion', async (action) => {
    const store = useAuthStore()
    const request = deferred<ReturnType<typeof auth>>()
    const mock = action === 'passkey' ? passkeyLogin : api[action]
    mock.mockReturnValueOnce(request.promise)
    const pending = (action === 'passkey' ? store.loginWithPasskey()
      : action === 'login2FA' ? store.login2FA('temporary-token', '123456')
        : store[action]({ email: alice.email, password: 'example-password' })).catch(error => error)
    await login(store, bob)
    request.resolve(auth(alice))
    await pending
    expect(store.token).toBe('access-2')
    expect(store.user?.id).toBe(2)
  })

  it('discards obsolete OAuth adoption without clearing pending OAuth metadata', async () => {
    const store = useAuthStore()
    const request = deferred<{ data: typeof alice }>()
    api.getCurrentUser.mockReturnValueOnce(request.promise)
    const pending = store.setToken('oauth-alice').catch(error => error)
    await login(store, bob)
    store.setPendingAuthSession({ token: 'pending-new', token_field: 'pending_auth_token', provider: 'oidc' })
    request.resolve({ data: alice })
    await pending
    expect(store.user?.id).toBe(2)
    expect(store.pendingAuthSession?.token).toBe('pending-new')
  })

  it('a newer pending OAuth flow invalidates an in-flight token adoption', async () => {
    const store = useAuthStore()
    const request = deferred<{ data: typeof alice }>()
    api.getCurrentUser.mockReturnValueOnce(request.promise)
    const pending = store.setToken('oauth-alice').catch(error => error)
    store.setPendingAuthSession({ token: 'pending-new', token_field: 'pending_auth_token', provider: 'oidc' })
    request.resolve({ data: alice })
    await pending
    expect(store.user).toBeNull()
    expect(store.pendingAuthSession?.token).toBe('pending-new')
  })

  it('does not write proactive refresh results into a later login', async () => {
    const store = useAuthStore()
    api.login.mockResolvedValueOnce({ ...auth(alice), expires_in: 121 })
    await store.login({ email: alice.email, password: 'example-password' })
    const request = deferred<{ access_token: string; refresh_token: string; expires_in: number }>()
    api.refreshToken.mockReturnValueOnce(request.promise)
    await vi.advanceTimersByTimeAsync(1000)
    await login(store, bob)
    request.resolve({ access_token: 'old-rotated-access', refresh_token: 'old-rotated-refresh', expires_in: 3600 })
    await request.promise
    await Promise.resolve()
    expect(store.token).toBe('access-2')
    expect(store.user?.id).toBe(2)
  })

  it('discards an old refresh after restoring another account from storage', async () => {
    const store = useAuthStore()
    await login(store, alice)
    const request = deferred<{ data: typeof alice }>()
    api.getCurrentUser.mockReturnValueOnce(request.promise)
    const pending = store.refreshUser().catch(error => error)
    localStorage.setItem('auth_token', 'access-2')
    localStorage.setItem('auth_user', JSON.stringify(bob))
    api.getCurrentUser.mockResolvedValueOnce({ data: bob })
    store.checkAuth()
    request.resolve({ data: alice })
    await pending
    expect(store.user?.id).toBe(2)
  })
})
