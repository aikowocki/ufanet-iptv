<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  adminCreateSubscription,
  adminListUserSubscriptions,
  adminUpdateSubscription,
  errorText,
  type AdminPackage,
  type AdminSubscription,
  type AdminUser,
} from '../lib/api'
import { formatDateTime, fromLocalInput, toLocalInput } from '../lib/datetime'

const props = defineProps<{ user: AdminUser | null; packages: AdminPackage[] }>()
const emit = defineEmits<{ close: [] }>()

const subs = ref<AdminSubscription[]>([])
const loading = ref(false)
const error = ref('')

const newPackage = ref<number | null>(null)
const newExpires = ref('')
const editing = ref<number | null>(null)
const editExpires = ref('')

const open = computed(() => props.user !== null)
const packageName = (id: number) => props.packages.find((p) => p.id === id)?.name ?? `#${id}`
const freePackages = computed(() =>
  props.packages.filter((p) => !subs.value.some((s) => s.package_id === p.id)),
)

async function load() {
  if (!props.user) return
  loading.value = true
  error.value = ''
  try {
    subs.value = await adminListUserSubscriptions(props.user.id)
  } catch (e) {
    error.value = errorText(e, 'Не удалось загрузить подписки')
  } finally {
    loading.value = false
  }
}

watch(
  () => props.user,
  () => {
    newPackage.value = null
    newExpires.value = ''
    editing.value = null
    subs.value = []
    load()
  },
)

async function run(action: () => Promise<unknown>) {
  error.value = ''
  try {
    await action()
    await load()
  } catch (e) {
    error.value = errorText(e, 'Операция не выполнена')
    await load()
  }
}

function add() {
  if (!props.user || newPackage.value === null) return
  const pkg = newPackage.value
  const expires = fromLocalInput(newExpires.value)
  run(async () => {
    await adminCreateSubscription(props.user!.id, { package_id: pkg, is_active: true, expires_at: expires })
    newPackage.value = null
    newExpires.value = ''
  })
}

const toggle = (s: AdminSubscription) => run(() => adminUpdateSubscription(s.id, { is_active: !s.is_active }))

function startEdit(s: AdminSubscription) {
  editing.value = s.id
  editExpires.value = toLocalInput(s.expires_at)
}

function saveEdit(s: AdminSubscription) {
  const expires = fromLocalInput(editExpires.value)
  run(async () => {
    await adminUpdateSubscription(s.id, { expires_at: expires })
    editing.value = null
  })
}
</script>

<template>
  <v-dialog :model-value="open" max-width="760" @update:model-value="emit('close')">
    <v-card v-if="user" :title="`Подписки: ${user.login}`">
      <v-card-text>
        <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-4">{{ error }}</v-alert>

        <v-table density="comfortable">
          <thead>
            <tr>
              <th>Пакет</th>
              <th>Включена</th>
              <th>Действует до</th>
              <th />
            </tr>
          </thead>
          <tbody>
            <tr v-if="!loading && subs.length === 0">
              <td colspan="4" class="text-medium-emphasis">Подписок нет</td>
            </tr>
            <tr v-for="s in subs" :key="s.id">
              <td>{{ packageName(s.package_id) }}</td>
              <td>
                <v-switch
                  :model-value="s.is_active"
                  color="primary"
                  density="compact"
                  hide-details
                  :aria-label="`Подписка на ${packageName(s.package_id)} включена`"
                  @update:model-value="toggle(s)"
                />
              </td>
              <td>
                <div v-if="editing === s.id" class="d-flex align-center ga-2 py-1">
                  <v-text-field
                    v-model="editExpires"
                    type="datetime-local"
                    label="Срок (пусто: бессрочно)"
                    density="compact"
                    hide-details
                  />
                  <v-btn icon="mdi-check" size="small" title="Сохранить" @click="saveEdit(s)" />
                  <v-btn icon="mdi-close" size="small" variant="text" title="Отмена" @click="editing = null" />
                </div>
                <span v-else>{{ formatDateTime(s.expires_at) }}</span>
              </td>
              <td class="text-right">
                <v-btn
                  v-if="editing !== s.id"
                  icon="mdi-pencil"
                  size="small"
                  variant="text"
                  title="Изменить срок"
                  @click="startEdit(s)"
                />
              </td>
            </tr>
          </tbody>
        </v-table>

        <h3 class="text-subtitle-1 mt-6 mb-2">Выдать пакет</h3>
        <div class="d-flex flex-wrap align-center ga-3">
          <v-select
            v-model="newPackage"
            :items="freePackages"
            item-title="name"
            item-value="id"
            label="Пакет"
            density="compact"
            hide-details
            style="min-width: 200px"
            no-data-text="Все пакеты уже выданы"
          />
          <v-text-field
            v-model="newExpires"
            type="datetime-local"
            label="Срок (пусто: бессрочно)"
            density="compact"
            hide-details
            style="min-width: 240px"
          />
          <v-btn color="primary" :disabled="newPackage === null" @click="add">Выдать</v-btn>
        </div>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="emit('close')">Закрыть</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>
