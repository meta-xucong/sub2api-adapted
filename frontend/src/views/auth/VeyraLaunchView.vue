<template>
  <div class="flex min-h-screen items-center justify-center bg-gray-50 px-6 dark:bg-dark-950">
    <div class="w-full max-w-sm text-center">
      <div class="mx-auto mb-5 h-9 w-9 animate-spin rounded-full border-2 border-primary-200 border-t-primary-600"></div>
      <h1 class="text-lg font-semibold text-gray-900 dark:text-white">正在进入 Alchemy</h1>
      <p v-if="errorMessage" class="mt-3 text-sm text-red-600 dark:text-red-300">{{ errorMessage }}</p>
      <p v-else class="mt-3 text-sm text-gray-500 dark:text-dark-400">正在建立安全登录会话。</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

interface PortalConfigResponse {
  data?: {
    alchemy_base_url?: string
  }
}

interface LoginTicketResponse {
  data?: {
    ticket?: string
  }
}

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const errorMessage = ref('')

function normalizedTarget(): 'alchemy' | 'alchemy-mobile' {
  return route.query.target === 'alchemy-mobile' ? 'alchemy-mobile' : 'alchemy'
}

function loginRedirect(): string {
  return `/veyra-launch?target=${normalizedTarget()}`
}

async function launchAlchemy(): Promise<void> {
  if (!authStore.token) {
    await router.replace({ path: '/login', query: { redirect: loginRedirect() } })
    return
  }

  try {
    const target = normalizedTarget()
    const configResponse = await fetch('/api/veyra/portal/config', { credentials: 'same-origin' })
    const configPayload = (await configResponse.json().catch(() => ({}))) as PortalConfigResponse
    const baseURL = configPayload.data?.alchemy_base_url?.trim().replace(/\/+$/, '') || 'https://alchemy.aiself.vip'

    const ticketResponse = await fetch('/api/veyra/login-ticket', {
      method: 'POST',
      credentials: 'same-origin',
      headers: {
        Authorization: `Bearer ${authStore.token}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ intent: 'alchemy' }),
    })
    const ticketPayload = (await ticketResponse.json().catch(() => ({}))) as LoginTicketResponse
    const ticket = ticketPayload.data?.ticket?.trim()

    if (!ticketResponse.ok || !ticket) {
      throw new Error('无法建立 Alchemy 登录会话，请重新登录后再试。')
    }

    const destination = new URL(target === 'alchemy-mobile' ? '/h5' : '/', baseURL)
    destination.searchParams.set('ticket', ticket)
    window.location.replace(destination.toString())
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '进入 Alchemy 失败，请稍后重试。'
  }
}

onMounted(() => {
  void launchAlchemy()
})
</script>
