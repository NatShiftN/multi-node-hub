<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  ApiError,
  banTrafficUser,
  getTrafficHistory,
  listTrafficUsers,
  setTrafficUserQuota,
  unbanTrafficUser,
} from '../api'
import { getUser, isSuperAdmin } from '../auth'
import type { TrafficDaily, TrafficUserSummary } from '../types'

const router = useRouter()
const users = ref<TrafficUserSummary[]>([])
const history = ref<TrafficDaily[]>([])
const loading = ref(true)
const error = ref('')
const notice = ref('')

const totalTraffic = computed(() => users.value.reduce((sum, user) => sum + user.total_bytes, 0))
const bannedCount = computed(() => users.value.filter((user) => user.is_banned).length)
const historyMax = computed(() => Math.max(1, ...history.value.map((day) => day.UploadBytes + day.DownloadBytes)))
const historyBars = computed(() =>
  history.value.map((day) => ({
    day: day.Day,
    total: day.UploadBytes + day.DownloadBytes,
    upload: day.UploadBytes,
    download: day.DownloadBytes,
    height: Math.max(2, Math.round(((day.UploadBytes + day.DownloadBytes) / historyMax.value) * 100)),
  })),
)

function formatBytes(value: number): string {
  if (!value) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024
    unit += 1
  }
  return `${amount.toFixed(1)} ${units[unit]}`
}

function messageOf(value: unknown): string {
  return value instanceof ApiError ? value.message : value instanceof Error ? value.message : String(value)
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const [userResponse, historyResponse] = await Promise.all([listTrafficUsers(), getTrafficHistory(30)])
    users.value = userResponse.users
    history.value = historyResponse.history || []
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    loading.value = false
  }
}

async function editQuota(user: TrafficUserSummary): Promise<void> {
  const input = window.prompt(`設定 ${user.user_email} 的配額（GB，0 = 無限）`, String((user.quota_bytes / 1024 ** 3).toFixed(2)))
  if (input === null) return
  const gb = Number(input)
  if (!Number.isFinite(gb) || gb < 0) {
    error.value = '配額必須是 0 或正數'
    return
  }
  try {
    await setTrafficUserQuota(user.user_email, Math.round(gb * 1024 ** 3))
    notice.value = `已更新 ${user.user_email} 的配額`
    await load()
  } catch (value) {
    error.value = messageOf(value)
  }
}

async function toggleBan(user: TrafficUserSummary): Promise<void> {
  const action = user.is_banned ? '解除封禁' : '封禁'
  if (!window.confirm(`${action} ${user.user_email}？`)) return
  try {
    if (user.is_banned) await unbanTrafficUser(user.user_email)
    else await banTrafficUser(user.user_email)
    await load()
  } catch (value) {
    error.value = messageOf(value)
  }
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
  <main class="relative mx-auto min-h-screen max-w-6xl px-4 py-8 sm:px-6">
    <header class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <p class="label text-emerald-400/80">Users / Subscriptions</p>
        <h1 class="mt-1 text-2xl font-semibold tracking-tight text-white">使用者與訂閱</h1>
        <p class="mt-2 text-xs text-slate-500">跨節點流量、配額、封禁狀態。訂閱連結由各節點的 Inbound 產生。</p>
      </div>
      <button class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-xs text-slate-300 hover:bg-white/[0.05]" @click="router.push({ name: 'dashboard' })">返回 Dashboard</button>
    </header>

    <div v-if="error" class="mt-4 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-xs text-rose-200">{{ error }}</div>
    <div v-if="notice" class="mt-4 rounded-lg border border-emerald-400/20 bg-emerald-400/[0.06] px-3 py-2 text-xs text-emerald-200">{{ notice }}</div>

    <section class="mt-5 grid grid-cols-1 gap-3 sm:grid-cols-3">
      <div class="panel p-4"><p class="label">Users</p><p class="mt-2 font-mono text-2xl text-white">{{ users.length }}</p></div>
      <div class="panel p-4"><p class="label">Total Traffic</p><p class="mt-2 font-mono text-2xl text-cyan-200">{{ formatBytes(totalTraffic) }}</p></div>
      <div class="panel p-4"><p class="label">Banned</p><p class="mt-2 font-mono text-2xl text-rose-300">{{ bannedCount }}</p></div>
    </section>

    <section class="panel mt-4 p-5">
      <div class="flex items-center justify-between">
        <h2 class="text-sm font-medium text-white">近 30 天流量</h2>
        <span class="font-mono text-[9px] text-slate-600">{{ historyBars.length }} DAYS</span>
      </div>
      <div v-if="historyBars.length === 0" class="mt-4 text-[11px] text-slate-500">尚無每日彙總資料。</div>
      <div v-else class="mt-4 flex h-32 items-end gap-1">
        <div v-for="bar in historyBars" :key="bar.day" class="group flex h-full flex-1 flex-col justify-end" :title="`${bar.day}: ${formatBytes(bar.total)}`">
          <div class="w-full rounded-t bg-gradient-to-t from-cyan-500/30 to-cyan-300/80" :style="{ height: `${bar.height}%` }"></div>
        </div>
      </div>
    </section>

    <section class="panel mt-4 overflow-hidden">
      <div v-if="loading" class="p-6 text-xs text-slate-500">載入中…</div>
      <div v-else-if="users.length === 0" class="p-8 text-center">
        <p class="text-sm text-slate-300">尚無使用者流量資料</p>
        <p class="mt-1 text-[11px] text-slate-600">建立 Inbound 並讓客戶端連線後，這裡會出現 Xray email 統計。</p>
      </div>
      <div v-else class="overflow-x-auto">
        <table class="w-full min-w-[800px] text-left text-xs">
          <thead class="border-b border-white/[0.06] text-[10px] uppercase tracking-[0.1em] text-slate-500">
            <tr><th class="px-5 py-3">Email</th><th class="px-4 py-3 text-right">Upload</th><th class="px-4 py-3 text-right">Download</th><th class="px-4 py-3 text-right">Total</th><th class="px-4 py-3 text-right">Quota</th><th class="px-4 py-3 text-center">Status</th><th class="px-5 py-3 text-right">Actions</th></tr>
          </thead>
          <tbody class="divide-y divide-white/[0.045]">
            <tr v-for="user in users" :key="user.user_email">
              <td class="px-5 py-3 font-mono text-slate-200">{{ user.user_email }}<span class="ml-2 text-[9px] text-slate-600">{{ user.node_count }} nodes</span></td>
              <td class="px-4 py-3 text-right font-mono text-emerald-300">{{ formatBytes(user.upload_bytes) }}</td>
              <td class="px-4 py-3 text-right font-mono text-sky-300">{{ formatBytes(user.download_bytes) }}</td>
              <td class="px-4 py-3 text-right font-mono text-white">{{ formatBytes(user.total_bytes) }}</td>
              <td class="px-4 py-3 text-right font-mono text-amber-300">{{ user.quota_bytes ? formatBytes(user.quota_bytes) : '∞' }}</td>
              <td class="px-4 py-3 text-center">
                <span class="rounded border px-1.5 py-0.5 text-[9px]" :class="user.is_banned ? 'border-rose-400/20 bg-rose-400/[0.06] text-rose-300' : 'border-emerald-400/20 bg-emerald-400/[0.06] text-emerald-300'">{{ user.is_banned ? 'BANNED' : 'ACTIVE' }}</span>
              </td>
              <td class="px-5 py-3">
                <div class="flex justify-end gap-1.5">
                  <button class="focus-ring rounded border border-white/[0.08] px-2 py-1 text-[10px] text-slate-300" @click="editQuota(user)">配額</button>
                  <button class="focus-ring rounded border px-2 py-1 text-[10px]" :class="user.is_banned ? 'border-emerald-400/20 text-emerald-300' : 'border-rose-400/20 text-rose-300'" @click="toggleBan(user)">{{ user.is_banned ? '解封' : '封禁' }}</button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </main>
</template>
