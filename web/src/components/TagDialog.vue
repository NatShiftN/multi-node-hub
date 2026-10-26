<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { ApiError, setNodeTags } from '../api'
import type { AgentNode } from '../types'

const props = defineProps<{ open: boolean; node: AgentNode | null }>()
const emit = defineEmits<{ close: []; saved: [] }>()

const tags = ref<string[]>([])
const input = ref('')
const error = ref('')
const saving = ref(false)
const inputElement = ref<HTMLInputElement | null>(null)

function messageOf(value: unknown): string {
  if (value instanceof ApiError) return value.message
  if (value instanceof Error) return value.message
  return String(value)
}

function addTag(): void {
  error.value = ''
  const parts = input.value
    .split(/[,\s]+/)
    .map((item) => item.trim())
    .filter(Boolean)
  if (parts.length === 0) return
  for (const tag of parts) {
    if (!/^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$/.test(tag)) {
      error.value = `標籤格式錯誤：${tag}`
      return
    }
    if (!tags.value.includes(tag)) tags.value.push(tag)
  }
  if (tags.value.length > 32) {
    tags.value = tags.value.slice(0, 32)
  }
  input.value = ''
}

function removeTag(tag: string): void {
  tags.value = tags.value.filter((item) => item !== tag)
}

function close(): void {
  if (!saving.value) emit('close')
}

async function save(): Promise<void> {
  if (!props.node || saving.value) return
  addTag()
  if (error.value) return
  saving.value = true
  try {
    await setNodeTags(props.node.id, tags.value)
    emit('saved')
    emit('close')
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    saving.value = false
  }
}

function onKeydown(event: KeyboardEvent): void {
  if (props.open && event.key === 'Escape') close()
}

watch(
  () => props.open,
  async (open) => {
    if (!open) return
    tags.value = [...(props.node?.tags ?? [])]
    input.value = ''
    error.value = ''
    await nextTick()
    inputElement.value?.focus()
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
          aria-label="管理節點標籤"
          class="relative w-full max-w-[520px] rounded-2xl border border-white/[0.08] bg-[#0b1119] p-5 shadow-panel sm:p-6"
        >
          <div class="flex items-start justify-between gap-4">
            <div>
              <p class="label text-cyan-400/80">Node Tags</p>
              <h3 class="mt-1 text-lg font-semibold text-white">標籤管理</h3>
              <p class="mt-1 font-mono text-[10px] text-slate-500">
                {{ node?.name }} <span class="text-slate-600">/</span> {{ node?.id }}
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

          <div class="mt-5 flex min-h-[48px] flex-wrap gap-1.5 rounded-xl border border-white/[0.06] bg-black/20 p-3">
            <span
              v-for="tag in tags"
              :key="tag"
              class="inline-flex items-center gap-1 rounded-md border border-cyan-400/15 bg-cyan-400/[0.06] px-2 py-1 font-mono text-[10px] text-cyan-200"
            >
              {{ tag }}
              <button
                type="button"
                class="text-cyan-400/50 transition hover:text-rose-300"
                :aria-label="`移除 ${tag}`"
                @click="removeTag(tag)"
              >
                ×
              </button>
            </span>
            <span v-if="tags.length === 0" class="self-center text-[11px] text-slate-600">尚未設定標籤</span>
          </div>

          <div class="mt-3 flex gap-2">
            <input
              ref="inputElement"
              v-model="input"
              type="text"
              spellcheck="false"
              placeholder="region:hk 或 type:relay"
              class="focus-ring min-w-0 flex-1 rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-cyan-400/40"
              :disabled="saving"
              @keydown.enter.prevent="addTag"
            />
            <button
              type="button"
              class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-[11px] text-slate-300 transition hover:bg-white/[0.05]"
              :disabled="saving"
              @click="addTag"
            >
              加入
            </button>
          </div>
          <p class="mt-2 text-[10px] leading-5 text-slate-600">
            可用格式：<code class="text-slate-400">region:hk</code>、<code class="text-slate-400">type:relay</code>；最多 32 個。
          </p>

          <div
            v-if="error"
            class="mt-3 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-[11px] text-rose-200"
            role="alert"
          >
            {{ error }}
          </div>

          <div class="mt-5 flex justify-end gap-2">
            <button
              type="button"
              class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-[11px] text-slate-400 transition hover:bg-white/[0.05]"
              :disabled="saving"
              @click="close"
            >
              取消
            </button>
            <button
              type="button"
              class="focus-ring rounded-lg bg-cyan-400 px-3 py-2 text-[11px] font-semibold text-cyan-950 transition hover:bg-cyan-300 disabled:opacity-60"
              :disabled="saving"
              @click="save"
            >
              {{ saving ? '儲存中…' : '儲存標籤' }}
            </button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
