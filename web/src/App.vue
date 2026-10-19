<script setup lang="ts">
import { watch } from 'vue'
import { useRouter } from 'vue-router'
import { clearCredentials, currentLogin, isAdmin, isLoggedIn } from './lib/auth'

const router = useRouter()

// Выход из любого места (включая сброс по 401) ведёт на экран входа.
watch(isLoggedIn, (ok) => {
  if (!ok) router.replace({ name: 'login' })
})

function logout() {
  clearCredentials()
}
</script>

<template>
  <v-app>
    <v-app-bar v-if="isLoggedIn" flat border>
      <v-app-bar-title>
        <v-icon icon="mdi-television-play" class="mr-2" />IPTV
      </v-app-bar-title>
      <v-btn :to="{ name: 'channels' }" prepend-icon="mdi-television-classic">Каналы</v-btn>
      <v-btn :to="{ name: 'subscriptions' }" prepend-icon="mdi-card-account-details-outline">Подписки</v-btn>
      <template v-if="isAdmin">
        <v-divider vertical class="mx-2" />
        <v-btn :to="{ name: 'admin-users' }" prepend-icon="mdi-account-group">Пользователи</v-btn>
        <v-btn :to="{ name: 'admin-channels' }" prepend-icon="mdi-cog-outline">Каналы (админ)</v-btn>
        <v-btn :to="{ name: 'admin-packages' }" prepend-icon="mdi-package-variant">Пакеты</v-btn>
      </template>
      <v-spacer />
      <span class="text-body-2 mr-2">{{ currentLogin }}</span>
      <v-btn icon="mdi-logout" title="Выйти" @click="logout" />
    </v-app-bar>

    <v-main>
      <v-container>
        <router-view />
      </v-container>
    </v-main>
  </v-app>
</template>
