<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import type { CommandResponse, SystemInfo } from '../types'

const props = defineProps<{
  open: boolean
  title: string
  loading: boolean
  result: CommandResponse | null
  error: string
}>()

const emit = defineEmits<{
  close: []
}>()

const closeButton = ref<HTMLButtonElement | null>(null)
let previousOverflow = ''

const info = computed<SystemInfo | null>(() => {
  const data = props.result?.data
  if (
    data !== null &&
    typeof data === 'object' &&
    'cpu_percent' in data &&
    'memory_total_bytes' in data
  ) {
    return data as SystemInfo
  }
  return null
})

const rawJSON = computed(() => {
  if (!props.result) return ''
  try {
    return JSON.stringify(props.result.data, null, 2)
  } catch {
    return String(props.result.data)
  }
})

const cpuBarWidth = computed(() => `${clampPercent(info.value?.cpu_percent)}%`)
const memoryBarWidth = computed(() => `${clampPercent(info.value?.memory_percent)}%`)

function clampPercent(value: number | undefined): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) return 0
  return Math.min(100, Math.max(0, value))
}

function formatPercent(value: number | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—'
  return `${value.toFixed(1)}%`
}

function formatBytes(value: number | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) return '—'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024
    unit += 1
  }
  const digits = unit === 0 ? 0 : amount >= 100 ? 0 : amount >= 10 ? 1 : 2
  return `${amount.toFixed(digits)} ${units[unit]}`
}

function formatUptime(seconds: number | undefined): string {
  if (typeof seconds !== 'number' || !Number.isFinite(seconds)) return '—'
  const total = Math.max(0, Math.floor(seconds))
  const days = Math.floor(total / 86_400)
  const hours = Math.floor((total % 86_400) / 3_600)
  const minutes = Math.floor((total % 3_600) / 60)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${minutes}m`
  return `${minutes}m`
}

function onKeydown(event: KeyboardEvent): void {
  if (props.open && event.key === 'Escape') {
    emit('close')
  }
}

watch(
  () => props.open,
  async (open) => {
    if (open) {
      previousOverflow = document.body.style.overflow
      document.body.style.overflow = 'hidden'
      await nextTick()
      closeButton.value?.focus()
    } else {
      document.body.style.overflow = previousOverflow
    }
  },
)

onMounted(() => window.addEventListener('keydown', onKeydown))
onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  document.body.style.overflow = previousOverflow
})
</script>

<template>
  <Teleport to="body">
    <Transition
      enter-active-class="transition duration-200 ease-out"
      enter-from-class="opacity-0"
      enter-to-class="opacity-100"
      leave-active-class="transition duration-150 ease-in"
      leave-from-class="opacity-100"
      leave-to-class="opacity-0"
    >
      <div v-if="open" class="fixed inset-0 z-50 overflow-y-auto">
        <div
          class="fixed inset-0 bg-black/75 backdrop-blur-sm"
          aria-hidden="true"
          @click="emit('close')"
        />
        <div class="relative flex min-h-full items-center justify-center p-4 sm:p-6">
          <div
            class="panel w-full max-w-2xl overflow-hidden border-white/[0.1] bg-[#0a0f18]"
            role="dialog"
            aria-modal="true"
            :aria-label="title"
          >
            <div class="h-px bg-gradient-to-r from-transparent via-emerald-400/70 to-transparent" />
            <header class="flex items-start justify-between gap-4 border-b border-white/[0.06] px-5 py-4 sm:px-6">
              <div class="min-w-0">
                <p class="label text-emerald-400/80">Plugin Result</p>
                <h2 class="mt-1.5 truncate text-base font-medium text-white sm:text-lg">
                  {{ title }}
                </h2>
              </div>
              <button
                ref="closeButton"
                type="button"
                class="focus-ring grid h-8 w-8 shrink-0 place-items-center rounded-lg border border-white/[0.07] text-slate-400 transition hover:bg-white/[0.05] hover:text-white"
                aria-label="關閉視窗"
                @click="emit('close')"
              >
                <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                  <path d="m6 6 12 12M18 6 6 18" stroke-linecap="round" />
                </svg>
              </button>
            </header>

            <div class="max-h-[75vh] overflow-y-auto px-5 py-5 sm:px-6">
              <div v-if="loading" class="flex flex-col items-center justify-center py-14">
                <div class="relative grid h-12 w-12 place-items-center">
                  <span class="absolute inset-0 animate-ping rounded-full border border-emerald-400/20" />
                  <svg viewBox="0 0 24 24" class="h-5 w-5 animate-spin text-emerald-300" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                    <path d="M21 12a9 9 0 1 1-6.2-8.56" stroke-linecap="round" />
                  </svg>
                </div>
                <p class="mt-4 text-sm text-slate-300">指令已下發，等待 Agent 回傳…</p>
                <p class="mt-1 font-mono text-[10px] tracking-[0.12em] text-slate-500">
                  COMMAND → WEBSOCKET → PLUGIN
                </p>
              </div>

              <div v-else-if="error" class="rounded-xl border border-rose-500/20 bg-rose-500/[0.06] p-4">
                <div class="flex items-center gap-2">
                  <span class="h-1.5 w-1.5 rounded-full bg-rose-400" />
                  <p class="text-xs font-semibold uppercase tracking-[0.14em] text-rose-300">
                    Agent Error
                  </p>
                </div>
                <p class="mt-3 break-words font-mono text-xs leading-5 text-rose-100/90">{{ error }}</p>
              </div>

              <template v-else-if="info">
                <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <div class="rounded-xl border border-white/[0.06] bg-white/[0.02] p-4">
                    <div class="flex items-center justify-between">
                      <p class="label">CPU Load</p>
                      <p class="font-mono text-sm tabular-nums text-emerald-300">
                        {{ formatPercent(info.cpu_percent) }}
                      </p>
                    </div>
                    <div class="mt-3 h-1.5 overflow-hidden rounded-full bg-white/[0.06]">
                      <div
                        class="h-full rounded-full bg-gradient-to-r from-emerald-500 to-teal-300 transition-all duration-500"
                        :style="{ width: cpuBarWidth }"
                      />
                    </div>
                    <p class="mt-3 truncate text-[11px] text-slate-500" :title="info.cpu_model">
                      {{ info.cpu_model?.trim() || 'Unknown CPU' }}
                    </p>
                  </div>

                  <div class="rounded-xl border border-white/[0.06] bg-white/[0.02] p-4">
                    <div class="flex items-center justify-between">
                      <p class="label">Memory</p>
                      <p class="font-mono text-sm tabular-nums text-sky-300">
                        {{ formatPercent(info.memory_percent) }}
                      </p>
                    </div>
                    <div class="mt-3 h-1.5 overflow-hidden rounded-full bg-white/[0.06]">
                      <div
                        class="h-full rounded-full bg-gradient-to-r from-sky-500 to-cyan-300 transition-all duration-500"
                        :style="{ width: memoryBarWidth }"
                      />
                    </div>
                    <p class="mt-3 font-mono text-[11px] text-slate-500">
                      {{ formatBytes(info.memory_used_bytes) }} / {{ formatBytes(info.memory_total_bytes) }}
                    </p>
                  </div>
                </div>

                <div class="mt-3 grid grid-cols-2 gap-3 sm:grid-cols-4">
                  <div class="rounded-lg border border-white/[0.05] bg-black/20 px-3 py-2.5">
                    <p class="label">Cores</p>
                    <p class="mt-1 font-mono text-sm text-slate-200">{{ info.cpu_cores }}</p>
                  </div>
                  <div class="rounded-lg border border-white/[0.05] bg-black/20 px-3 py-2.5">
                    <p class="label">Uptime</p>
                    <p class="mt-1 font-mono text-sm text-slate-200">{{ formatUptime(info.uptime_seconds) }}</p>
                  </div>
                  <div class="rounded-lg border border-white/[0.05] bg-black/20 px-3 py-2.5">
                    <p class="label">Platform</p>
                    <p class="mt-1 truncate font-mono text-sm text-slate-200" :title="info.platform">
                      {{ info.platform || info.os }}
                    </p>
                  </div>
                  <div class="rounded-lg border border-white/[0.05] bg-black/20 px-3 py-2.5">
                    <p class="label">Arch</p>
                    <p class="mt-1 font-mono text-sm text-slate-200">{{ info.arch }}</p>
                  </div>
                </div>

                <div class="mt-3 rounded-xl border border-white/[0.06]">
                  <div class="grid grid-cols-1 gap-px bg-white/[0.05] sm:grid-cols-2">
                    <div class="bg-[#0b111b] px-4 py-3">
                      <p class="label">Hostname</p>
                      <p class="mt-1 truncate font-mono text-xs text-slate-300">{{ info.hostname }}</p>
                    </div>
                    <div class="bg-[#0b111b] px-4 py-3">
                      <p class="label">Collected At</p>
                      <p class="mt-1 truncate font-mono text-xs text-slate-300">
                        {{ new Date(info.collected_at).toLocaleString() }}
                      </p>
                    </div>
                  </div>
                </div>

                <div v-if="info.warnings?.length" class="mt-3 rounded-lg border border-amber-400/15 bg-amber-400/[0.05] px-3 py-2.5">
                  <p class="label text-amber-300/80">Warnings</p>
                  <p v-for="warning in info.warnings" :key="warning" class="mt-1 font-mono text-[11px] text-amber-100/80">
                    {{ warning }}
                  </p>
                </div>

                <details class="group mt-3 rounded-xl border border-white/[0.06] bg-black/20">
                  <summary class="cursor-pointer select-none px-4 py-3 font-mono text-[10px] uppercase tracking-[0.14em] text-slate-500 transition hover:text-slate-300">
                    Raw JSON
                  </summary>
                  <pre class="max-h-64 overflow-auto border-t border-white/[0.05] p-4 font-mono text-[11px] leading-5 text-slate-400">{{ rawJSON }}</pre>
                </details>
              </template>

              <div v-else-if="result" class="rounded-xl border border-white/[0.06] bg-black/20 p-4">
                <p class="label">Raw Result</p>
                <pre class="mt-3 max-h-80 overflow-auto font-mono text-[11px] leading-5 text-slate-300">{{ rawJSON }}</pre>
              </div>
            </div>

            <footer
              v-if="result"
              class="flex flex-wrap items-center justify-between gap-2 border-t border-white/[0.06] px-5 py-3 sm:px-6"
            >
              <p class="max-w-[70%] truncate font-mono text-[10px] text-slate-600" :title="result.request_id">
                req {{ result.request_id }}
              </p>
              <p class="font-mono text-[10px] text-slate-500">
                RTT {{ result.duration_ms }}ms
              </p>
            </footer>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
