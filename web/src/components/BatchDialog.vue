<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { ApiError, listTags, runBatchCommand } from '../api'
import type { BatchCommandResponse, TagCount } from '../types'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

const available = ref<TagCount[]>([])
const selected = ref<string[]>([])
const allOnline = ref(false)
const plugin = ref('system_info')
const payload = ref('{}')
const timeoutSeconds = ref(60)
const loading = ref(false)
const error = ref('')
const result = ref<BatchCommandResponse | null>(null)
const payloadElement = ref<HTMLTextAreaElement | null>(null)

function messageOf(value: unknown): string {
  if (value instanceof ApiError) return value.message
  if (value instanceof Error) return value.message
  return String(value)
}

function toggleTag(tag: string): void {
  if (selected.value.includes(tag)) {
    selected.value = selected.value.filter((item) => item !== tag)
  } else {
    selected.value.push(tag)
  }
}

function close(): void {
  if (!loading.value) emit('close')
}

async function submit(): Promise<void> {
  if (loading.value) return
  error.value = ''
  result.value = null
  if (!allOnline.value && selected.value.length === 0) {
    error.value = '請選擇標籤，或勾選「全部在線節點」'
    return
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(payload.value)
  } catch {
    error.value = 'Payload 不是有效 JSON'
    return
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    error.value = 'Payload 必須是 JSON 物件'
    return
  }
  loading.value = true
  try {
    result.value = await runBatchCommand({
      tags: selected.value,
      all_online: allOnline.value,
      plugin: plugin.value,
      payload: parsed as Record<string, unknown>,
      timeout_ms: Math.max(1, Math.min(600, timeoutSeconds.value)) * 1000,
    })
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    loading.value = false
  }
}

function onKeydown(event: KeyboardEvent): void {
  if (props.open && event.key === 'Escape') close()
}

watch(
  () => props.open,
  async (open) => {
    if (!open) return
    selected.value = []
    allOnline.value = false
    plugin.value = 'system_info'
    payload.value = '{}'
    timeoutSeconds.value = 60
    error.value = ''
    result.value = null
    try {
      const response = await listTags()
      available.value = response.tags
      allOnline.value = response.tags.length === 0
    } catch (value) {
      error.value = messageOf(value)
    }
    await nextTick()
    payloadElement.value?.focus()
  },
)

onMounted(() => window.addEventListener('keydown', onKeydown))
onUnmounted(() => window.removeEventListener('keydown', onKeydown))
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
      <div v-if="open" class="fixed inset-0 z-[70] flex items-center justify-center p-4">
        <div class="absolute inset-0 bg-black/70 backdrop-blur-sm" @click="close"></div>
        <div
          role="dialog"
          aria-modal="true"
          aria-label="批量操作"
          class="relative max-h-[88vh] w-full max-w-[680px] overflow-y-auto rounded-2xl border border-white/[0.08] bg-[#0b1119] p-5 shadow-panel sm:p-6"
        >
          <div class="flex items-start justify-between gap-4">
            <div>
              <p class="label text-cyan-400/80">Batch Operations</p>
              <h3 class="mt-1 text-lg font-semibold text-white">批量操作</h3>
              <p class="mt-1 text-[11px] leading-5 text-slate-500">
                以標籤 AND 比對在線節點，並行下發插件命令。
              </p>
            </div>
            <button
              type="button"
              class="focus-ring grid h-8 w-8 place-items-center rounded-lg text-slate-500 transition hover:bg-white/[0.06] hover:text-white"
              aria-label="關閉"
              @click="close"
            >
              <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                <path d="M6 6l12 12M18 6 6 18" stroke-linecap="round" />
              </svg>
            </button>
          </div>

          <div class="mt-5">
            <label class="flex cursor-pointer items-start gap-3 rounded-xl border border-cyan-400/15 bg-cyan-400/[0.035] px-3.5 py-3">
              <input v-model="allOnline" type="checkbox" class="mt-0.5 h-3.5 w-3.5 rounded border-white/20 bg-black/30 text-cyan-400 focus:ring-cyan-400/40" />
              <span>
                <span class="block text-xs font-medium text-cyan-100">全部在線節點</span>
                <span class="mt-0.5 block text-[10px] text-slate-500">不需要標籤；會對目前所有 online 節點下發指令。</span>
              </span>
            </label>

            <p class="label mt-4">或依標籤選取</p>
            <div v-if="available.length === 0" class="mt-2 rounded-lg border border-white/[0.06] bg-white/[0.02] px-3 py-3 text-[11px] text-slate-500">
              尚無標籤；使用上方「全部在線節點」即可批次操作。
            </div>
            <div v-else class="mt-2 flex flex-wrap gap-2" :class="{ 'pointer-events-none opacity-40': allOnline }">
              <button
                v-for="tag in available"
                :key="tag.tag"
                type="button"
                class="focus-ring rounded-lg border px-2.5 py-1.5 font-mono text-[10px] transition"
                :class="
                  selected.includes(tag.tag)
                    ? 'border-cyan-400/30 bg-cyan-400/[0.1] text-cyan-200'
                    : 'border-white/[0.07] bg-white/[0.02] text-slate-500 hover:text-slate-300'
                "
                @click="toggleTag(tag.tag)"
              >
                {{ tag.tag }} <span class="ml-1 text-slate-600">{{ tag.count }}</span>
              </button>
            </div>
          </div>

          <div class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-[1.2fr_.8fr]">
            <label class="block">
              <span class="label">插件</span>
              <select
                v-model="plugin"
                class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-[#0b1119] px-3 py-2 text-xs text-slate-200 outline-none focus:border-cyan-400/40"
                :disabled="loading"
              >
                <option value="system_info">system_info</option>
                <option value="port_forward">port_forward</option>
                <option value="xray">xray</option>
                <option value="speedtest">speedtest</option>
              </select>
            </label>
            <label class="block">
              <span class="label">每節點 Timeout（秒）</span>
              <input
                v-model.number="timeoutSeconds"
                type="number"
                min="1"
                max="600"
                class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200 outline-none focus:border-cyan-400/40"
                :disabled="loading"
              />
            </label>
          </div>

          <label class="mt-4 block">
            <span class="label">Payload JSON</span>
            <textarea
              ref="payloadElement"
              v-model="payload"
              rows="7"
              spellcheck="false"
              class="focus-ring mt-1.5 w-full resize-y rounded-lg border border-white/[0.08] bg-black/30 p-3 font-mono text-[11px] leading-5 text-slate-200 outline-none transition focus:border-cyan-400/40"
              :disabled="loading"
            />
          </label>

          <div
            v-if="error"
            class="mt-3 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-[11px] leading-5 text-rose-200"
            role="alert"
          >
            {{ error }}
          </div>

          <div v-if="result" class="mt-4 rounded-xl border border-white/[0.06] bg-white/[0.02] p-3">
            <div class="flex flex-wrap items-center gap-4 font-mono text-[10px] text-slate-500">
              <span>MATCHED <b class="text-white">{{ result.matched }}</b></span>
              <span>SUCCESS <b class="text-emerald-300">{{ result.success.length }}</b></span>
              <span>FAILED <b class="text-rose-300">{{ result.failed.length }}</b></span>
              <span class="ml-auto">{{ (result.total_ms / 1000).toFixed(1) }}s</span>
            </div>
            <div v-if="result.success.length > 0" class="mt-2 max-h-32 overflow-y-auto">
              <p v-for="item in result.success" :key="item.node_id" class="font-mono text-[10px] leading-5 text-emerald-300/80">
                ✓ {{ item.name }} <span class="text-slate-600">({{ item.node_id }})</span>
              </p>
            </div>
            <div v-if="result.failed.length > 0" class="mt-2 max-h-32 overflow-y-auto">
              <p v-for="item in result.failed" :key="item.node_id" class="font-mono text-[10px] leading-5 text-rose-300/80">
                × {{ item.name }} <span class="text-slate-600">{{ item.error }}</span>
              </p>
            </div>
          </div>

          <div class="mt-5 flex justify-end gap-2">
            <button
              type="button"
              class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-[11px] text-slate-400 transition hover:bg-white/[0.05]"
              :disabled="loading"
              @click="close"
            >
              關閉
            </button>
            <button
              type="button"
              class="focus-ring inline-flex items-center gap-2 rounded-lg bg-cyan-400 px-3.5 py-2 text-[11px] font-semibold text-cyan-950 transition hover:bg-cyan-300 disabled:cursor-wait disabled:opacity-60"
              :disabled="loading"
              @click="submit"
            >
              <svg v-if="loading" viewBox="0 0 24 24" class="h-3.5 w-3.5 animate-spin" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M21 12a9 9 0 1 1-6.2-8.56" stroke-linecap="round" />
              </svg>
              {{ loading ? '執行中…' : '執行批量命令' }}
            </button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
