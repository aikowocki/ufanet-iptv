import { computed, ref } from 'vue'

const STORAGE_KEY = 'iptv.credentials'

interface Credentials {
  login: string
  password: string
}

function load(): Credentials | null {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY)
    return raw ? (JSON.parse(raw) as Credentials) : null
  } catch {
    return null
  }
}

const credentials = ref<Credentials | null>(load())

export const isLoggedIn = computed(() => credentials.value !== null)
export const currentLogin = computed(() => credentials.value?.login ?? '')

// btoa не умеет в не-latin1 символы, поэтому кодируем через UTF-8.
export function basicHeader(c: Credentials): string {
  const bytes = new TextEncoder().encode(`${c.login}:${c.password}`)
  return 'Basic ' + btoa(String.fromCharCode(...bytes))
}

export function authHeader(): string | null {
  return credentials.value ? basicHeader(credentials.value) : null
}

export function setCredentials(login: string, password: string): void {
  credentials.value = { login, password }
  // sessionStorage живёт до закрытия вкладки. Для учебного Basic Auth этого достаточно;
  // в боевом варианте нужны токены в HttpOnly-cookie.
  sessionStorage.setItem(STORAGE_KEY, JSON.stringify(credentials.value))
}

export function clearCredentials(): void {
  credentials.value = null
  sessionStorage.removeItem(STORAGE_KEY)
}
