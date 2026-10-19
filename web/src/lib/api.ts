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

async function request<T>(path: string, credentials?: { login: string; password: string }): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (credentials) {
    headers.Authorization = basicHeader(credentials)
  } else {
    const h = authHeader()
    if (h) headers.Authorization = h
  }

  let res: Response
  try {
    res = await fetch(path, { headers })
  } catch {
    throw new ApiError(0, 'network', 'Сервер недоступен')
  }

  if (!res.ok) {
    let code = 'error'
    let message = `Ошибка ${res.status}`
    try {
      const body = await res.json()
      code = body.error?.code ?? code
      message = body.error?.message ?? message
    } catch {
      /* тело не JSON, оставляем общий текст */
    }
    // Сессия недействительна (например, пароль сменили): возвращаем на экран входа.
    if (res.status === 401 && !credentials) clearCredentials()
    throw new ApiError(res.status, code, message)
  }
  return (await res.json()) as T
}

/** Проверяет логин и пароль запросом списка каналов. */
export async function verify(login: string, password: string): Promise<void> {
  await request('/api/v1/channels', { login, password })
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
