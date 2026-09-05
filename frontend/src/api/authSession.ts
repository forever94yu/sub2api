const AUTH_SESSION_KEY = 'auth_session_id'

export interface AuthSessionSnapshot {
  id: string | null
  userId: number | null
}

export function getStoredAuthUserId(): number | null {
  try {
    const id = JSON.parse(localStorage.getItem('auth_user') || 'null')?.id
    return typeof id === 'number' && Number.isFinite(id) && id > 0 ? id : null
  } catch {
    return null
  }
}

export function captureAuthSession(): AuthSessionSnapshot {
  return { id: localStorage.getItem(AUTH_SESSION_KEY), userId: getStoredAuthUserId() }
}

// Login/logout replace the session; rotating tokens and refreshing a profile do not.
export function beginAuthSession(): string {
  const id = Array.from(crypto.getRandomValues(new Uint32Array(4)), value => value.toString(16)).join('-')
  localStorage.setItem(AUTH_SESSION_KEY, id)
  return id
}

export function isCurrentAuthSession(session: AuthSessionSnapshot): boolean {
  const current = captureAuthSession()
  return current.id === session.id && current.userId === session.userId
}

export function authSessionChangedError() {
  return {
    status: 401,
    code: 'AUTH_SESSION_CHANGED',
    message: 'Authentication session changed while the request was in flight.'
  }
}
