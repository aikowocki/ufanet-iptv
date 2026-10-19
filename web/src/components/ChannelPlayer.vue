<script setup lang="ts">
import type Hls from 'hls.js'
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ApiError, playbackUrl, type Channel } from '../lib/api'

const props = defineProps<{ channel: Channel }>()
const emit = defineEmits<{
  /** Сервер ответил 403: подписка уже не действует, список каналов устарел. */
  forbidden: []
}>()

const video = ref<HTMLVideoElement | null>(null)
const loading = ref(false)
const error = ref('')
const link = ref('')
const copied = ref(false)

let hls: Hls | null = null
let refreshTimer: number | undefined
let retryTimer: number | undefined
let retries = 0
// Номер запуска: ответ на запрос ссылки прежнего канала не должен перебить текущий.
let generation = 0

// Ссылка живёт недолго (STREAM_TOKEN_TTL), а плейлист и сегменты подписаны на тот же срок.
// Поэтому за 30 секунд до конца берём у API новую ссылку и переключаем на неё поток.
const REFRESH_BEFORE_MS = 30_000
const MAX_RETRIES = 5

function expiresAt(url: string): number | null {
  const v = Number(new URL(url).searchParams.get('expires'))
  return Number.isFinite(v) && v > 0 ? v * 1000 : null
}

async function play(url: string, gen: number) {
  const el = video.value
  if (!el) return
  // hls.js весит около 500 КБ, поэтому грузим его только при первом просмотре.
  const { default: Hls } = await import('hls.js')
  if (gen !== generation || !video.value) return
  if (Hls.isSupported()) {
    if (!hls) {
      hls = new Hls({ liveSyncDurationCount: 3 })
      hls.on(Hls.Events.MANIFEST_PARSED, () => {
        retries = 0
        loading.value = false
        el.play().catch(() => {
          /* автозапуск мог быть заблокирован браузером: пользователь нажмёт play */
        })
      })
      hls.on(Hls.Events.ERROR, (_e, data) => onHlsError(data))
      hls.attachMedia(el)
    }
    hls.loadSource(url)
  } else if (el.canPlayType('application/vnd.apple.mpegurl')) {
    // Safari и iOS играют HLS сами.
    el.src = url
    el.play().catch(() => {})
    loading.value = false
  } else {
    loading.value = false
    error.value = 'Браузер не поддерживает воспроизведение HLS'
  }
}

function onHlsError(data: { fatal: boolean; type: string; response?: { code?: number } }) {
  if (!data.fatal) return
  if (data.response?.code === 403) {
    // Ссылка истекла: просим новую.
    void fetchLink(generation)
    return
  }
  if (data.type === 'networkError' && retries < MAX_RETRIES) {
    // Сервер мог ещё запускать поток (503 Retry-After) или была кратковременная ошибка сети.
    retries++
    window.clearTimeout(retryTimer)
    retryTimer = window.setTimeout(() => hls?.startLoad(), 1500)
    return
  }
  if (data.type === 'mediaError' && retries < MAX_RETRIES) {
    retries++
    hls?.recoverMediaError()
    return
  }
  loading.value = false
  error.value = 'Не удалось воспроизвести поток'
}

async function fetchLink(gen: number) {
  window.clearTimeout(refreshTimer)
  let url: string
  try {
    url = await playbackUrl(props.channel.id)
  } catch (e) {
    if (gen !== generation) return
    loading.value = false
    if (e instanceof ApiError && e.status === 403) emit('forbidden')
    else error.value = e instanceof ApiError ? e.message : 'Не удалось получить ссылку'
    return
  }
  if (gen !== generation) return
  link.value = url
  await play(url, gen)
  const exp = expiresAt(url)
  if (exp && gen === generation) {
    refreshTimer = window.setTimeout(() => fetchLink(gen), Math.max(exp - Date.now() - REFRESH_BEFORE_MS, 5_000))
  }
}

function start() {
  generation++
  window.clearTimeout(refreshTimer)
  window.clearTimeout(retryTimer)
  link.value = ''
  error.value = ''
  retries = 0
  loading.value = true
  void fetchLink(generation)
}

function destroy() {
  generation++
  window.clearTimeout(refreshTimer)
  window.clearTimeout(retryTimer)
  hls?.destroy()
  hls = null
}

// Смена канала использует тот же экземпляр hls.js и тот же <video>: без мигания чёрным экраном.
watch(() => props.channel.id, start)
onMounted(async () => {
  await nextTick()
  start()
})
onBeforeUnmount(destroy)

async function copy() {
  try {
    await navigator.clipboard.writeText(link.value)
    copied.value = true
    window.setTimeout(() => (copied.value = false), 2000)
  } catch {
    /* буфер недоступен: ссылку можно выделить вручную */
  }
}
</script>

<template>
  <div>
    <div class="player">
      <video ref="video" controls playsinline muted autoplay />
      <v-progress-circular v-if="loading" indeterminate class="player__spinner" />
    </div>
    <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mt-3">
      {{ error }}
      <template #append><v-btn variant="text" size="small" @click="start">Повторить</v-btn></template>
    </v-alert>
    <v-text-field
      v-if="link"
      :model-value="link"
      label="Ссылка на поток (HLS)"
      readonly
      density="compact"
      hide-details
      class="mt-3"
      :append-inner-icon="copied ? 'mdi-check' : 'mdi-content-copy'"
      @click:append-inner="copy"
    />
  </div>
</template>

<style scoped>
.player {
  position: relative;
  aspect-ratio: 16 / 9;
  background: #000;
  border-radius: 4px;
  overflow: hidden;
}
.player video {
  width: 100%;
  height: 100%;
}
.player__spinner {
  position: absolute;
  inset: 0;
  margin: auto;
}
</style>
