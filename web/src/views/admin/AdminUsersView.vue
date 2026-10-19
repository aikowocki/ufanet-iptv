<script setup lang="ts">
import { onMounted, ref } from 'vue'
import SubscriptionsDialog from '../../components/SubscriptionsDialog.vue'
import {
  adminCreateUser,
  adminListPackages,
  adminListUsers,
  adminUpdateUser,
  errorText,
  type AdminPackage,
  type AdminUser,
} from '../../lib/api'
import { currentLogin } from '../../lib/auth'

const users = ref<AdminUser[]>([])
const packages = ref<AdminPackage[]>([])
const loading = ref(true)
const error = ref('')
const snack = ref('')

const headers = [
  { title: 'ID', key: 'id', width: 80 },
  { title: 'Логин', key: 'login' },
  { title: 'Активен', key: 'is_active', sortable: false },
  { title: 'Админ', key: 'is_admin', sortable: false },
  { title: '', key: 'actions', sortable: false, align: 'end' },
] as const

async function load() {
  loading.value = true
  error.value = ''
  try {
    ;[users.value, packages.value] = await Promise.all([adminListUsers(), adminListPackages()])
  } catch (e) {
    error.value = errorText(e, 'Не удалось загрузить пользователей')
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function patch(u: AdminUser, data: Parameters<typeof adminUpdateUser>[1]) {
  try {
    const updated = await adminUpdateUser(u.id, data)
    Object.assign(u, updated)
  } catch (e) {
    snack.value = errorText(e, 'Не удалось сохранить')
    await load()
  }
}

// Создание
const createOpen = ref(false)
const form = ref({ login: '', password: '', is_admin: false })
const formError = ref('')
const saving = ref(false)

function openCreate() {
  form.value = { login: '', password: '', is_admin: false }
  formError.value = ''
  createOpen.value = true
}

async function create() {
  saving.value = true
  formError.value = ''
  try {
    await adminCreateUser({ ...form.value, is_active: true })
    createOpen.value = false
    snack.value = 'Пользователь создан'
    await load()
  } catch (e) {
    formError.value = errorText(e, 'Не удалось создать пользователя')
  } finally {
    saving.value = false
  }
}

// Смена пароля
const pwUser = ref<AdminUser | null>(null)
const newPassword = ref('')
const pwError = ref('')

function openPassword(u: AdminUser) {
  pwUser.value = u
  newPassword.value = ''
  pwError.value = ''
}

async function savePassword() {
  if (!pwUser.value) return
  saving.value = true
  pwError.value = ''
  try {
    await adminUpdateUser(pwUser.value.id, { password: newPassword.value })
    pwUser.value = null
    snack.value = 'Пароль изменён'
  } catch (e) {
    pwError.value = errorText(e, 'Не удалось изменить пароль')
  } finally {
    saving.value = false
  }
}

const subsUser = ref<AdminUser | null>(null)
</script>

<template>
  <div class="d-flex align-center mb-4">
    <h1 class="text-h5">Пользователи</h1>
    <v-spacer />
    <v-btn color="primary" prepend-icon="mdi-plus" @click="openCreate">Создать</v-btn>
  </div>

  <v-alert v-if="error" type="error" variant="tonal" class="mb-4">
    {{ error }}
    <template #append><v-btn variant="text" @click="load">Повторить</v-btn></template>
  </v-alert>
  <v-data-table v-else :headers="headers" :items="users" :loading="loading" no-data-text="Пользователей нет">
    <template #[`item.is_active`]="{ item }">
      <v-switch
        :model-value="item.is_active"
        color="primary"
        density="compact"
        hide-details
        :disabled="item.login === currentLogin"
        :aria-label="`${item.login}: активен`"
        @update:model-value="patch(item, { is_active: !item.is_active })"
      />
    </template>
    <template #[`item.is_admin`]="{ item }">
      <v-switch
        :model-value="item.is_admin"
        color="primary"
        density="compact"
        hide-details
        :disabled="item.login === currentLogin"
        :aria-label="`${item.login}: администратор`"
        @update:model-value="patch(item, { is_admin: !item.is_admin })"
      />
    </template>
    <template #[`item.actions`]="{ item }">
      <v-btn icon="mdi-card-account-details-outline" variant="text" title="Подписки" @click="subsUser = item" />
      <v-btn icon="mdi-key-variant" variant="text" title="Сменить пароль" @click="openPassword(item)" />
    </template>
  </v-data-table>

  <v-dialog v-model="createOpen" max-width="440">
    <v-card title="Новый пользователь">
      <v-card-text>
        <v-form @submit.prevent="create">
          <v-text-field v-model="form.login" label="Логин" autocomplete="off" autofocus
            hint="Латиница, цифры, . _ -, от 3 до 64 символов" persistent-hint />
          <v-text-field v-model="form.password" label="Пароль" type="password" autocomplete="new-password"
            hint="От 8 символов" persistent-hint class="mt-2" />
          <v-switch v-model="form.is_admin" label="Администратор" color="primary" hide-details />
          <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mt-2">{{ formError }}</v-alert>
        </v-form>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="createOpen = false">Отмена</v-btn>
        <v-btn color="primary" :loading="saving" :disabled="!form.login || !form.password" @click="create">
          Создать
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <v-dialog :model-value="pwUser !== null" max-width="420" @update:model-value="pwUser = null">
    <v-card :title="`Новый пароль: ${pwUser?.login ?? ''}`">
      <v-card-text>
        <v-text-field v-model="newPassword" label="Пароль" type="password" autocomplete="new-password"
          hint="От 8 символов" persistent-hint autofocus />
        <v-alert v-if="pwError" type="error" variant="tonal" density="compact" class="mt-2">{{ pwError }}</v-alert>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="pwUser = null">Отмена</v-btn>
        <v-btn color="primary" :loading="saving" :disabled="!newPassword" @click="savePassword">Сохранить</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <SubscriptionsDialog :user="subsUser" :packages="packages" @close="subsUser = null" />

  <v-snackbar :model-value="snack !== ''" timeout="4000" @update:model-value="snack = ''">{{ snack }}</v-snackbar>
</template>
