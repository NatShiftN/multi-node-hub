<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, createAPIKey, listAPIKeys, revokeAPIKey } from '../api'
import { getUser, isSuperAdmin } from '../auth'
import type { AdminAPIKey } from '../types'

const router = useRouter()
const keys = ref<AdminAPIKey[]>([])
const name = ref('')
const newToken = ref('')
const loading = ref(true)
const saving = ref(false)
const error = ref('')
const notice = ref('')

function messageOf(value: unknown): string {
  return value instanceof ApiError ? value.message : value instanceof Error ? value.message : String(value)
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    keys.value = (await listAPIKeys()).api_keys
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    loading.value = false
  }
}

async function create(): Promise<void> {
  if (saving.value) return
  saving.value = true
  error.value = ''
  newToken.value = ''
  try {
    const response = await createAPIKey(name.value.trim() || 'Merchant API Key')
    newToken.value = response.token
    name.value = ''
    await load()
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    saving.value = false
  }
}

async function revoke(key: AdminAPIKey): Promise<void> {
  if (!window.confirm(`撤銷 ${key.name}？`)) return
  try {
    await revokeAPIKey(key.id)
    await load()
  } catch (value) {
    error.value = messageOf(value)
  }
}

async function copyToken(): Promise<void> {
  await navigator.clipboard.writeText(newToken.value)
  notice.value = 'Token 已複製'
}

onMounted(() => {
  if (!isSuperAdmin(getUser())) {
    void router.replace({ name: 'dashboard' })
    return
  }
  void load()
})
</script>

<template>
  <main class="relative mx-auto min-h-screen max-w-5xl px-4 py-8 sm:px-6">
    <header class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <p class="label text-violet-400/80">Developer API</p>
        <h1 class="mt-1 text-2xl font-semibold tracking-tight text-white">API Keys</h1>
        <p class="mt-2 text-xs text-slate-500">商家 Bearer Token；只顯示一次完整 token，之後僅保留 hash。</p>
      </div>
      <button class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-xs text-slate-300 hover:bg-white/[0.05]" @click="router.push({ name: 'dashboard' })">返回 Dashboard</button>
    </header>

    <div v-if="error" class="mt-4 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-xs text-rose-200">{{ error }}</div>
    <div v-if="notice" class="mt-4 rounded-lg border border-emerald-400/20 bg-emerald-400/[0.06] px-3 py-2 text-xs text-emerald-200">{{ notice }}</div>

    <section class="panel mt-5 p-5">
      <h2 class="text-sm font-medium text-white">建立商家 Key</h2>
      <div class="mt-3 flex flex-wrap gap-2">
        <input v-model="name" placeholder="Xboard production" class="focus-ring min-w-[240px] flex-1 rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 text-xs text-slate-200" />
        <button class="focus-ring rounded-lg bg-violet-400 px-4 py-2 text-xs font-semibold text-violet-950 disabled:opacity-50" :disabled="saving" @click="create">{{ saving ? '建立中…' : '建立 API Key' }}</button>
      </div>
      <div v-if="newToken" class="mt-4 rounded-lg border border-amber-400/20 bg-amber-400/[0.05] p-3">
        <p class="text-[10px] text-amber-200">只顯示一次，請立即保存：</p>
        <div class="mt-2 flex items-center gap-2">
          <code class="min-w-0 flex-1 truncate rounded bg-black/30 px-2 py-1.5 font-mono text-[10px] text-amber-100">{{ newToken }}</code>
          <button class="focus-ring rounded border border-amber-400/20 px-2 py-1 text-[10px] text-amber-200" @click="copyToken">複製</button>
        </div>
      </div>
    </section>

    <section class="panel mt-4 overflow-hidden">
      <div class="border-b border-white/[0.06] px-5 py-4"><h2 class="text-sm font-medium text-white">已建立的 Keys</h2></div>
      <div v-if="loading" class="p-6 text-xs text-slate-500">載入中…</div>
      <div v-else-if="keys.length === 0" class="p-6 text-xs text-slate-500">尚未建立 API Key。</div>
      <div v-else class="divide-y divide-white/[0.045]">
        <div v-for="key in keys" :key="key.id" class="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
          <div>
            <p class="text-sm text-slate-200">{{ key.name }}</p>
            <p class="mt-1 font-mono text-[10px] text-slate-500">{{ key.prefix }}… · {{ key.revoked ? 'REVOKED' : 'ACTIVE' }} · {{ key.last_used_at || 'never used' }}</p>
          </div>
          <button v-if="!key.revoked" class="focus-ring rounded border border-rose-400/20 px-2.5 py-1 text-[10px] text-rose-300" @click="revoke(key)">撤銷</button>
          <span v-else class="text-[10px] text-slate-600">已撤銷</span>
        </div>
      </div>
    </section>
  </main>
</template>
