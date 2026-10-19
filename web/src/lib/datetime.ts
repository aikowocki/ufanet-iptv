/** ISO-строка в значение для <input type="datetime-local"> (локальное время, без секунд). */
export function toLocalInput(iso: string | null): string {
  if (!iso) return ''
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** Значение datetime-local в ISO (UTC); пустая строка означает «бессрочно». */
export function fromLocalInput(value: string): string | null {
  return value ? new Date(value).toISOString() : null
}

export function formatDateTime(iso: string | null): string {
  return iso ? new Date(iso).toLocaleString('ru-RU') : 'Бессрочно'
}
