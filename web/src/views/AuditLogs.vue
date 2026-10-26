<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, listAuditLogs } from '../api'
import { getUser } from '../auth'
import UIModeToggle from '../components/UIModeToggle.vue'
import type { AuditLog } from '../types'

const router = useRouter()
const currentUser = getUser()
const logs = ref<AuditLog[]>([])
const page = ref(1)
const pageSize = 50
const total = ref(0)
const totalPages = ref(0)
const loading = ref(true)
const error = ref('')

const rangeLabel = computed(() => {
  if (total.value === 0) return '0'
  const start = (page.value - 1) * pageSize + 1
  const end = Math.min(page.value * pageSize, total.value)
  return `${start}–${end} / ${total.value}`
})

function messageOf(value: unknown): string {
  if (value instanceof ApiError) return value.message
  if (value instanceof Error) return value.message
  return String(value)
}

async function load(target = page.value): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const response = await listAuditLogs(target, pageSize)
    logs.value = response.logs
    page.value = response.page
    total.value = response.total
    totalPages.value = response.total_pages
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    loading.value = false
  }
}

function go(next: number): void {
  if (next < 1 || (totalPages.value > 0 && next > totalPages.value) || loading.value) return
  void load(next)
}

function actionClasses(action: string): string {
  if (action.includes('delete') || action.includes('replace') || action.includes('ban')) {
    return 'border-rose-400/20 bg-rose-400/[0.06] text-rose-300'
  }
  if (action.includes('auto_heal') || action.includes('backup')) {
    return 'border-amber-400/20 bg-amber-400/[0.06] text-amber-300'
  }
  if (action.includes('xray') || action.includes('batch') || action.includes('cloud')) {
    return 'border-sky-400/20 bg-sky-400/[0.06] text-sky-300'
  }
  return 'border-slate-500/20 bg-slate-500/[0.06] text-slate-400'
}

function formatDetails(details: string): string {
  if (!details) return '—'
  try {
    const parsed = JSON.parse(details) as Record<string, unknown>
    return Object.entries(parsed)
      .map(([key, value]) => `${key}=${typeof value === 'string' ? value : JSON.stringify(value)}`)
      .join(' · ')
  } catch {
    return details
  }
}

function formatTimestamp(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString()
}

onMounted(() => void load(1))
</script>

<template>
  <div class="relative min-h-screen overflow-hidden">
    <div class="pointer-events-none absolute inset-x-0 top-0 h-72 bg-[radial-gradient(ellipse_at_top,rgba(16,185,129,.08),transparent_70%)]"></div>
    <header class="relative border-b border-white/[0.06]">
      <div class="mx-auto flex max-w-7xl items-center justify-between px-5 py-5 sm:px-8">
        <div class="flex items-center gap-3">
          <div class="grid h-10 w-10 place-items-center rounded-xl border border-emerald-400/20 bg-emerald-400/[0.07]">
            <svg viewBox="0 0 24 24" class="h-5 w-5 text-emerald-300" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
              <circle cx="12" cy="5" r="2.2" />
              <circle cx="5" cy="18" r="2.2" />
              <circle cx="19" cy="18" r="2.2" />
              <path d="M10.6 6.7 6.3 16M13.4 6.7l4.3 9.3M7.2 18h9.6" stroke-linecap="round" />
            </svg>
          </div>
          <div>
            <p class="font-mono text-[9px] uppercase tracking-[0.28em] text-slate-500">Governance</p>
            <p class="text-lg font-semibold tracking-tight text-white">NODEHUB</p>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <span v-if="currentUser" class="hidden font-mono text-[10px] text-slate-500 sm:block">
            {{ currentUser.username }}
          </span>
          <UIModeToggle />
          <button
            type="button"
            class="focus-ring inline-flex items-center gap-2 rounded-lg border border-white/[0.08] bg-white/[0.035] px-3 py-2 text-xs font-medium text-slate-300 transition hover:bg-white/[0.06]"
            @click="router.push({ name: 'dashboard' })"
          >
            <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
              <path d="M15 6l-6 6 6 6" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
            返回面板
          </button>
        </div>
      </div>
    </header>

    <main class="relative mx-auto max-w-7xl px-5 py-8 sm:px-8">
      <div class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="label text-amber-400/80">Audit Trail</p>
          <h1 class="mt-2 text-2xl font-semibold tracking-tight text-white sm:text-3xl">操作日誌</h1>
          <p class="mt-2 text-sm text-slate-400">
            追蹤管理員與 AutoHeal / 配額系統的破壞性操作。
          </p>
        </div>
        <button
          type="button"
          class="focus-ring inline-flex items-center gap-2 rounded-lg border border-white/[0.08] px-3 py-2 text-xs text-slate-400 transition hover:bg-white/[0.05] hover:text-white disabled:opacity-40"
          :disabled="loading"
          @click="load(page)"
        >
          <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" :class="{ 'animate-spin': loading }" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
            <path d="M20 11a8 8 0 1 0-2.34 5.66M20 4v7h-7" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
          重新整理
        </button>
      </div>

      <div
        v-if="error"
        class="mt-5 rounded-xl border border-rose-500/20 bg-rose-500/[0.06] px-4 py-3 text-sm text-rose-200"
        role="alert"
      >
        {{ error }}
      </div>

      <section class="mt-6 overflow-hidden rounded-2xl border border-white/[0.07] bg-[#0a0f18]/90 shadow-panel backdrop-blur-sm">
        <div class="overflow-x-auto">
          <table class="w-full min-w-[1040px] border-collapse text-left">
            <thead>
              <tr class="border-b border-white/[0.05] text-[10px] uppercase tracking-[0.14em] text-slate-500">
                <th class="px-4 py-3 font-medium">時間</th>
                <th class="px-4 py-3 font-medium">操作者</th>
                <th class="px-4 py-3 font-medium">Action</th>
                <th class="px-4 py-3 font-medium">Target</th>
                <th class="px-4 py-3 font-medium">IP</th>
                <th class="px-4 py-3 font-medium">Details</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-white/[0.045]">
              <template v-if="loading && logs.length === 0">
                <tr v-for="row in 5" :key="row" class="animate-pulse">
                  <td v-for="col in 6" :key="col" class="px-4 py-4">
                    <div class="h-3 rounded bg-slate-800/80" :class="col === 6 ? 'w-56' : 'w-24'"></div>
                  </td>
                </tr>
              </template>
              <tr v-else-if="logs.length === 0">
                <td colspan="6" class="px-5 py-16 text-center">
                  <div class="mx-auto grid h-11 w-11 place-items-center rounded-full border border-white/[0.06] bg-white/[0.02]">
                    <svg viewBox="0 0 24 24" class="h-4 w-4 text-slate-600" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                      <path d="M5 4h14v16H5zM8 8h8M8 12h6M8 16h4" stroke-linecap="round" />
                    </svg>
                  </div>
                  <p class="mt-3 text-xs font-medium text-slate-400">尚無操作日誌</p>
                  <p class="mt-1 text-[10px] text-slate-600">寫入性 API 與 AutoHeal 會自動記錄。</p>
                </td>
              </tr>
              <template v-else>
              <tr
                v-for="entry in logs"
                :key="entry.id"
                class="align-top transition-colors hover:bg-white/[0.02]"
              >
                <td class="whitespace-nowrap px-4 py-3 font-mono text-[10px] text-slate-400">
                  {{ formatTimestamp(entry.timestamp) }}
                </td>
                <td class="px-4 py-3 font-mono text-[11px] text-slate-300">{{ entry.username }}</td>
                <td class="px-4 py-3">
                  <span class="inline-block rounded border px-1.5 py-0.5 font-mono text-[9px]" :class="actionClasses(entry.action)">
                    {{ entry.action }}
                  </span>
                </td>
                <td class="px-4 py-3 font-mono text-[10px] text-slate-400">{{ entry.target_node_id || '—' }}</td>
                <td class="px-4 py-3 font-mono text-[10px] text-slate-500">{{ entry.ip_address || '—' }}</td>
                <td class="max-w-[360px] px-4 py-3">
                  <p class="truncate font-mono text-[10px] text-slate-500" :title="formatDetails(entry.details)">
                    {{ formatDetails(entry.details) }}
                  </p>
                </td>
              </tr>
              </template>
            </tbody>
          </table>
        </div>
        <footer class="flex flex-wrap items-center justify-between gap-3 border-t border-white/[0.06] px-4 py-3">
          <p class="font-mono text-[10px] text-slate-600">{{ rangeLabel }} ENTRIES</p>
          <div class="flex items-center gap-2">
            <button
              type="button"
              class="focus-ring rounded-lg border border-white/[0.08] px-3 py-1.5 text-[10px] text-slate-400 transition hover:bg-white/[0.05] disabled:opacity-30"
              :disabled="page <= 1 || loading"
              @click="go(page - 1)"
            >
              上一頁
            </button>
            <span class="font-mono text-[10px] text-slate-500">{{ page }} / {{ Math.max(totalPages, 1) }}</span>
            <button
              type="button"
              class="focus-ring rounded-lg border border-white/[0.08] px-3 py-1.5 text-[10px] text-slate-400 transition hover:bg-white/[0.05] disabled:opacity-30"
              :disabled="page >= totalPages || loading"
              @click="go(page + 1)"
            >
              下一頁
            </button>
          </div>
        </footer>
      </section>
    </main>
  </div>
</template>
