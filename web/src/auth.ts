const TOKEN_KEY = 'nodehub.token'
const USER_KEY = 'nodehub.user'
const EXPIRES_KEY = 'nodehub.expires_at'

import type { AdminRole } from './types'

export interface AuthUser {
  id: number
  username: string
  role: AdminRole
  allowed_tags: string[]
}

export function isSuperAdmin(user: AuthUser | null = getUser()): boolean {
  return user?.role === 'superadmin'
}

export function canWrite(user: AuthUser | null = getUser()): boolean {
  return user?.role === 'superadmin' || user?.role === 'operator'
}

export function allowedTags(user: AuthUser | null = getUser()): string[] {
  return user?.allowed_tags ?? []
}

let memoryToken: string | null = null
let memoryUser: AuthUser | null = null
let memoryExpires: string | null = null

export function getToken(): string | null {
  try {
    const stored = localStorage.getItem(TOKEN_KEY)
    if (stored) return stored
  } catch {
    // Fall back to the current-tab session below.
  }
  return memoryToken
}

export function getUser(): AuthUser | null {
  try {
    const raw = localStorage.getItem(USER_KEY)
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<AuthUser>
      if (typeof parsed.id === 'number' && typeof parsed.username === 'string') {
        const role: AdminRole =
          parsed.role === 'operator' || parsed.role === 'viewer' ? parsed.role : 'superadmin'
        return {
          id: parsed.id,
          username: parsed.username,
          role,
          allowed_tags: Array.isArray(parsed.allowed_tags)
            ? parsed.allowed_tags.filter((tag): tag is string => typeof tag === 'string')
            : [],
        }
      }
    }
  } catch {
    // Fall back to the current-tab session below.
  }
  return memoryUser
}

export function setSession(token: string, user: AuthUser, expiresAt: string): void {
  memoryToken = token
  memoryUser = user
  memoryExpires = expiresAt
  try {
    localStorage.setItem(TOKEN_KEY, token)
    localStorage.setItem(USER_KEY, JSON.stringify(user))
    localStorage.setItem(EXPIRES_KEY, expiresAt)
  } catch {
    // Storage can be unavailable in privacy mode; the memory session keeps
    // this tab usable until reload.
  }
}

export function clearSession(): void {
  memoryToken = null
  memoryUser = null
  memoryExpires = null
  try {
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(USER_KEY)
    localStorage.removeItem(EXPIRES_KEY)
  } catch {
    // Ignore storage failures.
  }
}

export function hasValidSession(): boolean {
  const token = getToken()
  if (!token) return false
  let expiresRaw: string | null = null
  try {
    expiresRaw = localStorage.getItem(EXPIRES_KEY) ?? memoryExpires
  } catch {
    expiresRaw = memoryExpires
  }
  if (!expiresRaw) return true
  const expires = new Date(expiresRaw).getTime()
  if (Number.isFinite(expires) && expires <= Date.now()) {
    clearSession()
    return false
  }
  return true
}
