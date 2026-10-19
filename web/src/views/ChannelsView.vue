<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ChannelPlayer from '../components/ChannelPlayer.vue'
import { ApiError, listChannels, type Channel } from '../lib/api'

const LAST_CHANNEL_KEY = 'iptv.lastChannel'

const channels = ref<Channel[]>([])
const loading = ref(true)
const error = ref('')
const search = ref('')
const onlyAvailable = ref(false)
const selectedId = ref<number | null>(null)

const snackbar = ref(false)
const snackbarText = ref('')
function notify(text: string) {
  snackbarText.value = text
  snackbar.value = true
}

const selected = computed(() => channels.value.find((c) => c.id === selectedId.value) ?? null)
const availableChannels = computed(() => channels.value.filter((c) => c.available))
const availableCount = computed(() => availableChannels.value.length)

const visible = computed(() => {
  const q = search.value.trim().toLowerCase()
  return channels.value.filter(
    (c) =>
      (!onlyAvailable.value || c.available) &&
      (!q || c.name.toLowerCase().includes(q) || String(c.number).includes(q)),
  )
})

function select(channel: Channel) {
  selectedId.value = channel.id
  localStorage.setItem(LAST_CHANNEL_KEY, String(channel.id))
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    channels.value = await listChannels()
    if (!selected.value) {
      // Первый запуск или канал пропал из списка: берём последний просмотренный или первый доступный.
      const last = Number(localStorage.getItem(LAST_CHANNEL_KEY))
      const initial =
        channels.value.find((c) => c.id === last && c.available) ??
        availableChannels.value[0] ??
        channels.value[0]
      if (initial) select(initial)
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'Не удалось загрузить каналы'
  } finally {
    loading.value = false
  }
}

/** Переключение на соседний доступный канал по кругу; закрытые подпиской пропускаются. */
function step(delta: 1 | -1) {
  const list = availableChannels.value
  if (!list.length) return
  const i = list.findIndex((c) => c.id === selectedId.value)
  // Если выбран закрытый канал, «вперёд» берёт ближайший доступный с большим номером.
  let next: Channel | undefined
  if (i >= 0) {
    next = list[(i + delta + list.length) % list.length]
  } else {
    const num = selected.value?.number ?? 0
    next =
      delta === 1
        ? (list.find((c) => c.number > num) ?? list[0])
        : ([...list].reverse().find((c) => c.number < num) ?? list[list.length - 1])
  }
  if (next) select(next)
}

// Набор номера цифрами, как на пульте: "7" переключит на канал 7 через 1,2 секунды после последней цифры.
const dialed = ref('')
let dialTimer: number | undefined

function dial(digit: string) {
  dialed.value += digit
  window.clearTimeout(dialTimer)
  dialTimer = window.setTimeout(() => {
    const number = Number(dialed.value)
    dialed.value = ''
    const target = channels.value.find((c) => c.number === number)
    if (target) select(target)
    else notify(`Канала ${number} нет`)
  }, 1200)
}

function onKey(e: KeyboardEvent) {
  const el = e.target as HTMLElement | null
  // В полях ввода (поиск, ссылка) клавиши работают как обычно.
  if (el && ['INPUT', 'TEXTAREA'].includes(el.tagName)) return
  if (e.ctrlKey || e.metaKey || e.altKey) return
  if (e.key === 'PageUp') {
    e.preventDefault()
    step(-1)
  } else if (e.key === 'PageDown') {
    e.preventDefault()
    step(1)
  } else if (/^\d$/.test(e.key)) {
    dial(e.key)
  }
}

onMounted(() => {
  load()
  window.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  window.clearTimeout(dialTimer)
})

async function onForbidden() {
  // Подписка истекла уже после загрузки списка: обновляем его, и плеер сам сменится заглушкой.
  notify('Подписка на этот канал не действует')
  await load()
}
</script>

<template>
  <div class="d-flex flex-wrap align-center ga-4 mb-4">
    <h1 class="text-h5">Каналы</h1>
    <v-chip v-if="!loading && !error" color="primary" variant="tonal">
      Доступно {{ availableCount }} из {{ channels.length }}
    </v-chip>
  </div>

  <v-alert v-if="error" type="error" variant="tonal" class="mb-4">
    {{ error }}
    <template #append><v-btn variant="text" @click="load">Повторить</v-btn></template>
  </v-alert>

  <v-progress-linear v-if="loading && !channels.length" indeterminate />

  <v-row v-else-if="channels.length">
    <!-- Плеер и переключение -->
    <v-col cols="12" md="8">
      <v-card v-if="selected">
        <v-card-item>
          <template #prepend>
            <v-avatar :color="selected.available ? 'primary' : 'grey-darken-2'" size="48">
              {{ selected.number }}
            </v-avatar>
          </template>
          <v-card-title>{{ selected.name }}</v-card-title>
          <v-card-subtitle>
            <v-icon :icon="selected.available ? 'mdi-broadcast' : 'mdi-lock'" size="small" />
            {{ selected.available ? 'В эфире' : 'Нужна подписка' }}
          </v-card-subtitle>
          <template #append>
            <v-chip v-if="dialed" color="warning" variant="flat" class="mr-2">Канал {{ dialed }}_</v-chip>
            <v-btn-group density="comfortable" variant="tonal" color="primary" divided>
              <v-btn
                icon="mdi-skip-previous"
                title="Предыдущий канал (PageUp)"
                :disabled="availableCount < 1"
                @click="step(-1)"
              />
              <v-btn
                icon="mdi-skip-next"
                title="Следующий канал (PageDown)"
                :disabled="availableCount < 1"
                @click="step(1)"
              />
            </v-btn-group>
          </template>
        </v-card-item>

        <v-card-text>
          <ChannelPlayer v-if="selected.available" :channel="selected" @forbidden="onForbidden" />
          <div v-else class="locked d-flex flex-column align-center justify-center ga-3">
            <v-icon icon="mdi-lock" size="64" />
            <div class="text-h6">Канал «{{ selected.name }}» не входит в вашу подписку</div>
            <v-btn
              color="warning"
              prepend-icon="mdi-cart-outline"
              @click="notify('Оформите подписку у оператора: онлайн-оплаты в сервисе пока нет')"
            >
              Оформить подписку
            </v-btn>
          </div>
          <div class="text-caption text-medium-emphasis mt-3">
            PageUp и PageDown переключают каналы, цифры набирают номер канала.
          </div>
        </v-card-text>
      </v-card>
    </v-col>

    <!-- Список каналов -->
    <v-col cols="12" md="4">
      <v-card>
        <v-card-text class="pb-0">
          <v-text-field
            v-model="search"
            label="Поиск"
            prepend-inner-icon="mdi-magnify"
            density="compact"
            hide-details
            clearable
          />
          <v-switch
            v-model="onlyAvailable"
            label="Только доступные"
            color="primary"
            density="compact"
            hide-details
          />
        </v-card-text>
        <v-list class="channel-list" lines="one" nav>
          <v-list-item
            v-for="c in visible"
            :key="c.id"
            :active="c.id === selectedId"
            color="primary"
            :title="c.name"
            @click="select(c)"
          >
            <template #prepend>
              <v-avatar :color="c.available ? 'primary' : 'grey-darken-2'" size="32" class="mr-3">
                <span class="text-caption">{{ c.number }}</span>
              </v-avatar>
            </template>
            <template #append>
              <v-icon v-if="!c.available" icon="mdi-lock" size="small" title="Нужна подписка" />
              <v-icon v-else-if="c.id === selectedId" icon="mdi-play-circle" size="small" />
            </template>
          </v-list-item>
          <v-list-item v-if="!visible.length" title="Ничего не найдено" />
        </v-list>
      </v-card>
    </v-col>
  </v-row>

  <v-snackbar v-model="snackbar" timeout="4000">{{ snackbarText }}</v-snackbar>
</template>

<style scoped>
.channel-list {
  max-height: 60vh;
  overflow-y: auto;
}
.locked {
  aspect-ratio: 16 / 9;
  background: rgba(var(--v-theme-on-surface), 0.06);
  border-radius: 4px;
  text-align: center;
  padding: 16px;
}
</style>
