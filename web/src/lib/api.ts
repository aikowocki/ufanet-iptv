import { authHeader, basicHeader, clearCredentials } from './auth'

export interface Channel {
  id: number
  number: number
  name: string
  /** false: нужна подписка. */
  available: boolean
}

export type SubscriptionStatus = 'active' | 'expired' | 'disabled'

export interface Subscription {
  package_id: number
  package: string
  status: SubscriptionStatus
  /** null: подписка бессрочная. */
  expires_at: string | null
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
  }
}

interface RequestOptions {
  method?: string
  body?: unknown
  credentials?: { login: string; password: string }
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (opts.credentials) {
    headers.Authorization = basicHeader(opts.credentials)
  } else {
    const h = authHeader()
    if (h) headers.Authorization = h
  }
  let body: string | undefined
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.body)
  }

  let res: Response
  try {
    res = await fetch(path, { method: opts.method ?? 'GET', headers, body })
  } catch {
    throw new ApiError(0, 'network', 'Сервер недоступен')
  }
  if (!res.ok) {
    let code = 'error'
    let message = `Ошибка ${res.status}`
    try {
      const data = await res.json()
      code = data.error?.code ?? code
      message = data.error?.message ?? message
    } catch {
      /* тело не JSON, оставляем общий текст */
    }
    // Сессия недействительна (например, пароль сменили): возвращаем на экран входа.
    if (res.status === 401 && !opts.credentials) clearCredentials()
    throw new ApiError(res.status, code, message)
  }
  return (await res.json()) as T
}

/** Проверяет логин и пароль запросом списка каналов. */
export async function verify(login: string, password: string): Promise<void> {
  await request('/api/v1/channels', { credentials: { login, password } })
}

export async function listChannels(): Promise<Channel[]> {
  return (await request<{ channels: Channel[] }>('/api/v1/channels')).channels
}

export async function listSubscriptions(): Promise<Subscription[]> {
  return (await request<{ subscriptions: Subscription[] }>('/api/v1/subscriptions')).subscriptions
}

export async function playbackUrl(channelId: number): Promise<string> {
  return (await request<{ url: string }>(`/api/v1/channels/${channelId}/playback`)).url
}

// ---- Админ-API ----

export interface AdminUser {
  id: number
  login: string
  is_active: boolean
  is_admin: boolean
}

export interface AdminChannel {
  id: number
  number: number
  name: string
  stream_path: string
  is_active: boolean
}

export interface AdminPackage {
  id: number
  name: string
  is_active: boolean
  channel_ids: number[]
}

export interface AdminSubscription {
  id: number
  user_id: number
  package_id: number
  is_active: boolean
  /** null: подписка бессрочная. */
  expires_at: string | null
  created_at: string
}

const ADMIN = '/api/v1/admin'

/** Админ ли пользователь: обычному абоненту сервер ответит 403. */
export async function probeAdmin(login: string, password: string): Promise<boolean> {
  try {
    await request(`${ADMIN}/users?limit=1`, { credentials: { login, password } })
    return true
  } catch (e) {
    if (e instanceof ApiError && e.status === 403) return false
    throw e
  }
}

export async function adminListUsers(limit = 200, offset = 0): Promise<AdminUser[]> {
  return (await request<{ users: AdminUser[] }>(`${ADMIN}/users?limit=${limit}&offset=${offset}`)).users
}

export function adminCreateUser(data: {
  login: string
  password: string
  is_admin: boolean
  is_active: boolean
}): Promise<AdminUser> {
  return request(`${ADMIN}/users`, { method: 'POST', body: data })
}

export function adminUpdateUser(
  id: number,
  data: Partial<{ is_active: boolean; is_admin: boolean; password: string }>,
): Promise<AdminUser> {
  return request(`${ADMIN}/users/${id}`, { method: 'PATCH', body: data })
}

export async function adminListChannels(): Promise<AdminChannel[]> {
  return (await request<{ channels: AdminChannel[] }>(`${ADMIN}/channels`)).channels
}

export function adminCreateChannel(data: Omit<AdminChannel, 'id'>): Promise<AdminChannel> {
  return request(`${ADMIN}/channels`, { method: 'POST', body: data })
}

export function adminUpdateChannel(id: number, data: Partial<Omit<AdminChannel, 'id'>>): Promise<AdminChannel> {
  return request(`${ADMIN}/channels/${id}`, { method: 'PATCH', body: data })
}

export async function adminListPackages(): Promise<AdminPackage[]> {
  return (await request<{ packages: AdminPackage[] }>(`${ADMIN}/packages`)).packages
}

export function adminCreatePackage(data: { name: string; is_active: boolean }): Promise<AdminPackage> {
  return request(`${ADMIN}/packages`, { method: 'POST', body: data })
}

export function adminUpdatePackage(id: number, data: Partial<{ name: string; is_active: boolean }>): Promise<AdminPackage> {
  return request(`${ADMIN}/packages/${id}`, { method: 'PATCH', body: data })
}

export function adminSetPackageChannels(id: number, channelIds: number[]): Promise<AdminPackage> {
  return request(`${ADMIN}/packages/${id}/channels`, { method: 'PUT', body: { channel_ids: channelIds } })
}

export async function adminListUserSubscriptions(userId: number): Promise<AdminSubscription[]> {
  return (await request<{ subscriptions: AdminSubscription[] }>(`${ADMIN}/users/${userId}/subscriptions`))
    .subscriptions
}

export function adminCreateSubscription(
  userId: number,
  data: { package_id: number; is_active: boolean; expires_at?: string | null },
): Promise<AdminSubscription> {
  return request(`${ADMIN}/users/${userId}/subscriptions`, { method: 'POST', body: data })
}

export function adminUpdateSubscription(
  id: number,
  data: Partial<{ is_active: boolean; expires_at: string | null }>,
): Promise<AdminSubscription> {
  return request(`${ADMIN}/subscriptions/${id}`, { method: 'PATCH', body: data })
}

/** Текст ошибки для показа пользователю. */
export function errorText(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback
}
