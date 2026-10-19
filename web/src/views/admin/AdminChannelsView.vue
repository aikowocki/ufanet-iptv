<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  adminCreateChannel,
  adminListChannels,
  adminUpdateChannel,
  errorText,
  type AdminChannel,
} from '../../lib/api'

const channels = ref<AdminChannel[]>([])
const loading = ref(true)
const error = ref('')
const snack = ref('')

const headers = [
  { title: '№', key: 'number', width: 80 },
  { title: 'Название', key: 'name' },
  { title: 'Путь потока', key: 'stream_path' },
  { title: 'Включён', key: 'is_active', sortable: false },
  { title: '', key: 'actions', sortable: false, align: 'end' },
] as const

async function load() {
  loading.value = true
  error.value = ''
  try {
    channels.value = await adminListChannels()
  } catch (e) {
    error.value = errorText(e, 'Не удалось загрузить каналы')
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function toggle(c: AdminChannel) {
  try {
    Object.assign(c, await adminUpdateChannel(c.id, { is_active: !c.is_active }))
  } catch (e) {
    snack.value = errorText(e, 'Не удалось сохранить')
    await load()
  }
}

// Диалог создания и редактирования: editId === null означает создание.
const dialog = ref(false)
const editId = ref<number | null>(null)
const form = ref({ number: 1, name: '', stream_path: '', is_active: true })
const formError = ref('')
const saving = ref(false)

function openCreate() {
  const next = channels.value.reduce((m, c) => Math.max(m, c.number), 0) + 1
  editId.value = null
  form.value = { number: next, name: '', stream_path: '', is_active: true }
  formError.value = ''
  dialog.value = true
}

function openEdit(c: AdminChannel) {
  editId.value = c.id
  form.value = { number: c.number, name: c.name, stream_path: c.stream_path, is_active: c.is_active }
  formError.value = ''
  dialog.value = true
}

async function save() {
  saving.value = true
  formError.value = ''
  try {
    const data = { ...form.value, number: Number(form.value.number) }
    if (editId.value === null) await adminCreateChannel(data)
    else await adminUpdateChannel(editId.value, data)
    dialog.value = false
    await load()
  } catch (e) {
    formError.value = errorText(e, 'Не удалось сохранить канал')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="d-flex align-center mb-4">
    <h1 class="text-h5">Каналы</h1>
    <v-spacer />
    <v-btn color="primary" prepend-icon="mdi-plus" @click="openCreate">Добавить</v-btn>
  </div>

  <v-alert v-if="error" type="error" variant="tonal" class="mb-4">
    {{ error }}
    <template #append><v-btn variant="text" @click="load">Повторить</v-btn></template>
  </v-alert>
  <v-data-table
    v-else
    :headers="headers"
    :items="channels"
    :loading="loading"
    :sort-by="[{ key: 'number', order: 'asc' }]"
    no-data-text="Каналов нет"
  >
    <template #[`item.is_active`]="{ item }">
      <v-switch
        :model-value="item.is_active"
        color="primary"
        density="compact"
        hide-details
        :aria-label="`${item.name}: включён`"
        @update:model-value="toggle(item)"
      />
    </template>
    <template #[`item.actions`]="{ item }">
      <v-btn icon="mdi-pencil" variant="text" title="Изменить" @click="openEdit(item)" />
    </template>
  </v-data-table>

  <v-dialog v-model="dialog" max-width="480">
    <v-card :title="editId === null ? 'Новый канал' : 'Изменить канал'">
      <v-card-text>
        <v-form @submit.prevent="save">
          <v-text-field v-model.number="form.number" label="Номер" type="number" min="1" />
          <v-text-field v-model="form.name" label="Название" />
          <v-text-field v-model="form.stream_path" label="Путь потока" placeholder="/live/9/index.m3u8"
            hint="Относительно адреса стрим-сервера" persistent-hint />
          <v-switch v-model="form.is_active" label="Включён" color="primary" hide-details class="mt-2" />
          <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mt-2">{{ formError }}</v-alert>
        </v-form>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="dialog = false">Отмена</v-btn>
        <v-btn color="primary" :loading="saving" :disabled="!form.name || !form.stream_path" @click="save">
          Сохранить
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <v-snackbar :model-value="snack !== ''" timeout="4000" @update:model-value="snack = ''">{{ snack }}</v-snackbar>
</template>
