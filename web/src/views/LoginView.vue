<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, verify } from '../lib/api'
import { setCredentials } from '../lib/auth'

const router = useRouter()
const login = ref('')
const password = ref('')
const loading = ref(false)
const error = ref('')

async function submit() {
  loading.value = true
  error.value = ''
  try {
    await verify(login.value, password.value)
    setCredentials(login.value, password.value)
    await router.replace({ name: 'channels' })
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) error.value = 'Неверный логин или пароль'
    else if (e instanceof ApiError && e.status === 429) error.value = 'Слишком много попыток, попробуйте позже'
    else if (e instanceof ApiError && e.status === 503) error.value = 'Сервис перегружен, повторите через секунду'
    else error.value = e instanceof ApiError ? e.message : 'Не удалось войти'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <v-row justify="center" class="mt-12">
    <v-col cols="12" sm="8" md="5" lg="4">
      <v-card title="Вход" subtitle="Логин и пароль абонента">
        <v-card-text>
          <v-form @submit.prevent="submit">
            <v-text-field v-model="login" label="Логин" autocomplete="username" autofocus required />
            <v-text-field
              v-model="password"
              label="Пароль"
              type="password"
              autocomplete="current-password"
              required
            />
            <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-4">
              {{ error }}
            </v-alert>
            <v-btn type="submit" color="primary" block :loading="loading" :disabled="!login || !password">
              Войти
            </v-btn>
          </v-form>
        </v-card-text>
      </v-card>
    </v-col>
  </v-row>
</template>
