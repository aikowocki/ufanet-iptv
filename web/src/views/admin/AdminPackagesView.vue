<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  adminCreatePackage,
  adminListChannels,
  adminListPackages,
  adminSetPackageChannels,
  adminUpdatePackage,
  errorText,
  type AdminChannel,
  type AdminPackage,
} from '../../lib/api'

const packages = ref<AdminPackage[]>([])
const channels = ref<AdminChannel[]>([])
const loading = ref(true)
const error = ref('')
const snack = ref('')

const headers = [
  { title: 'Название', key: 'name' },
  { title: 'Каналов', key: 'channel_ids', sortable: false },
  { title: 'Включён', key: 'is_active', sortable: false },
  { title: '', key: 'actions', sortable: false, align: 'end' },
] as const

const channelItems = computed(() =>
  [...channels.value].sort((a, b) => a.number - b.number).map((c) => ({ id: c.id, title: `${c.number}. ${c.name}` })),
)

async function load() {
  loading.value = true
  error.value = ''
  try {
    ;[packages.value, channels.value] = await Promise.all([adminListPackages(), adminListChannels()])
  } catch (e) {
    error.value = errorText(e, 'Не удалось загрузить пакеты')
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function toggle(p: AdminPackage) {
  try {
    Object.assign(p, await adminUpdatePackage(p.id, { is_active: !p.is_active }))
  } catch (e) {
    snack.value = errorText(e, 'Не удалось сохранить')
    await load()
  }
}

// Диалог: editId === null означает создание. Состав задаётся отдельным запросом PUT.
const dialog = ref(false)
const editId = ref<number | null>(null)
const form = ref({ name: '', is_active: true, channel_ids: [] as number[] })
const formError = ref('')
const saving = ref(false)

function openCreate() {
  editId.value = null
  form.value = { name: '', is_active: true, channel_ids: [] }
  formError.value = ''
  dialog.value = true
}

function openEdit(p: AdminPackage) {
  editId.value = p.id
  form.value = { name: p.name, is_active: p.is_active, channel_ids: [...p.channel_ids] }
  formError.value = ''
  dialog.value = true
}

async function save() {
  saving.value = true
  formError.value = ''
  try {
    let id = editId.value
    if (id === null) {
      id = (await adminCreatePackage({ name: form.value.name, is_active: form.value.is_active })).id
    } else {
      await adminUpdatePackage(id, { name: form.value.name, is_active: form.value.is_active })
    }
    await adminSetPackageChannels(id, form.value.channel_ids)
    dialog.value = false
    await load()
  } catch (e) {
    formError.value = errorText(e, 'Не удалось сохранить пакет')
    // Пакет мог создаться до сбоя на составе: переходим к его редактированию, а не к повторному созданию.
    await load()
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="d-flex align-center mb-4">
    <h1 class="text-h5">Пакеты</h1>
    <v-spacer />
    <v-btn color="primary" prepend-icon="mdi-plus" @click="openCreate">Создать</v-btn>
  </div>

  <v-alert v-if="error" type="error" variant="tonal" class="mb-4">
    {{ error }}
    <template #append><v-btn variant="text" @click="load">Повторить</v-btn></template>
  </v-alert>
  <v-data-table v-else :headers="headers" :items="packages" :loading="loading" no-data-text="Пакетов нет">
    <template #[`item.channel_ids`]="{ item }">{{ item.channel_ids.length }}</template>
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

  <v-dialog v-model="dialog" max-width="520">
    <v-card :title="editId === null ? 'Новый пакет' : 'Изменить пакет'">
      <v-card-text>
        <v-form @submit.prevent="save">
          <v-text-field v-model="form.name" label="Название" />
          <v-select
            v-model="form.channel_ids"
            :items="channelItems"
            item-title="title"
            item-value="id"
            label="Каналы пакета"
            multiple
            chips
            closable-chips
          />
          <v-switch v-model="form.is_active" label="Включён" color="primary" hide-details />
          <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mt-2">{{ formError }}</v-alert>
        </v-form>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="dialog = false">Отмена</v-btn>
        <v-btn color="primary" :loading="saving" :disabled="!form.name" @click="save">Сохранить</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <v-snackbar :model-value="snack !== ''" timeout="4000" @update:model-value="snack = ''">{{ snack }}</v-snackbar>
</template>
