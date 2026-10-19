<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ApiError, listSubscriptions, type Subscription, type SubscriptionStatus } from '../lib/api'

const subscriptions = ref<Subscription[]>([])
const loading = ref(true)
const error = ref('')
const filter = ref<'all' | 'active'>('all')

const headers = [
  { title: 'Пакет', key: 'package' },
  { title: 'Статус', key: 'status' },
  { title: 'Действует до', key: 'expires_at' },
] as const

const statusView: Record<SubscriptionStatus, { text: string; color: string; icon: string }> = {
  active: { text: 'Действует', color: 'success', icon: 'mdi-check-circle' },
  expired: { text: 'Истекла', color: 'warning', icon: 'mdi-clock-alert-outline' },
  disabled: { text: 'Выключена', color: 'error', icon: 'mdi-cancel' },
}

const items = computed(() =>
  filter.value === 'active' ? subscriptions.value.filter((s) => s.status === 'active') : subscriptions.value,
)
const activeCount = computed(() => subscriptions.value.filter((s) => s.status === 'active').length)

function formatExpires(s: Subscription): string {
  if (!s.expires_at) return 'Бессрочно'
  return new Date(s.expires_at).toLocaleString('ru-RU')
}

async function load(silent = false) {
  if (!silent) loading.value = true
  error.value = ''
  try {
    subscriptions.value = await listSubscriptions()
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'Не удалось загрузить подписки'
  } finally {
    loading.value = false
  }
}

// Срок подписки считает сервер, поэтому периодически перечитываем статус:
// так истёкшая подписка сама меняет вид без перезагрузки страницы.
let timer: number | undefined
onMounted(() => {
  load()
  timer = window.setInterval(() => load(true), 30_000)
})
onBeforeUnmount(() => window.clearInterval(timer))
</script>

<template>
  <div class="d-flex flex-wrap align-center ga-4 mb-4">
    <h1 class="text-h5">Подписки</h1>
    <v-chip v-if="!loading && !error" color="primary" variant="tonal">
      Действует {{ activeCount }} из {{ subscriptions.length }}
    </v-chip>
    <v-spacer />
    <v-btn-toggle v-model="filter" mandatory color="primary" density="compact" variant="outlined" divided>
      <v-btn value="all">Все</v-btn>
      <v-btn value="active">Действующие</v-btn>
    </v-btn-toggle>
  </div>

  <v-alert v-if="error" type="error" variant="tonal" class="mb-4">
    {{ error }}
    <template #append><v-btn variant="text" @click="load()">Повторить</v-btn></template>
  </v-alert>

  <v-data-table
    v-else
    :headers="headers"
    :items="items"
    :loading="loading"
    item-value="package_id"
    no-data-text="Подписок нет"
    hide-default-footer
  >
    <template #[`item.status`]="{ item }">
      <v-chip :color="statusView[item.status].color" :prepend-icon="statusView[item.status].icon" variant="tonal">
        {{ statusView[item.status].text }}
      </v-chip>
    </template>
    <template #[`item.expires_at`]="{ item }">{{ formatExpires(item) }}</template>
  </v-data-table>
</template>
