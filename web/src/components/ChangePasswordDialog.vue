<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { ApiError, changePassword } from '../api'

const props = defineProps<{ open: boolean; username: string }>()
const emit = defineEmits<{ close: [] }>()

const current = ref('')
const next = ref('')
const confirm = ref('')
const error = ref('')
const success = ref('')
const saving = ref(false)
const currentInput = ref<HTMLInputElement | null>(null)

function messageOf(value: unknown): string {
  if (value instanceof ApiError) return value.message
  if (value instanceof Error) return value.message
  return String(value)
}

function close(): void {
  if (!saving.value) emit('close')
}

async function submit(): Promise<void> {
  if (saving.value) return
  error.value = ''
  success.value = ''
  if (next.value.length < 8) {
    error.value = '新密碼至少需要 8 個字元'
    return
  }
  if (next.value !== confirm.value) {
    error.value = '兩次輸入的新密碼不一致'
    return
  }
  saving.value = true
  try {
    await changePassword(current.value, next.value)
    success.value = '密碼已更新'
    current.value = ''
    next.value = ''
    confirm.value = ''
    window.setTimeout(() => emit('close'), 700)
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
    current.value = ''
    next.value = ''
    confirm.value = ''
    error.value = ''
    success.value = ''
    await nextTick()
    currentInput.value?.focus()
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
          aria-label="變更管理員密碼"
          class="relative w-full max-w-[420px] rounded-2xl border border-white/[0.08] bg-[#0b1119] p-5 shadow-panel sm:p-6"
        >
          <div class="flex items-start justify-between gap-4">
            <div>
              <p class="label text-amber-400/80">Admin Account</p>
              <h3 class="mt-1 text-lg font-semibold text-white">變更密碼</h3>
              <p class="mt-1 font-mono text-[10px] text-slate-500">{{ username }}</p>
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

          <form class="mt-5 space-y-3" @submit.prevent="submit">
            <label class="block">
              <span class="label">目前密碼</span>
              <input
                ref="currentInput"
                v-model="current"
                type="password"
                autocomplete="current-password"
                class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 text-sm text-slate-100 outline-none transition focus:border-amber-400/40"
                :disabled="saving"
              />
            </label>
            <label class="block">
              <span class="label">新密碼</span>
              <input
                v-model="next"
                type="password"
                autocomplete="new-password"
                class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 text-sm text-slate-100 outline-none transition focus:border-amber-400/40"
                :disabled="saving"
              />
            </label>
            <label class="block">
              <span class="label">確認新密碼</span>
              <input
                v-model="confirm"
                type="password"
                autocomplete="new-password"
                class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 text-sm text-slate-100 outline-none transition focus:border-amber-400/40"
                :disabled="saving"
              />
            </label>

            <div
              v-if="error"
              class="rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-[11px] leading-5 text-rose-200"
              role="alert"
            >
              {{ error }}
            </div>
            <div
              v-if="success"
              class="rounded-lg border border-emerald-400/20 bg-emerald-400/[0.06] px-3 py-2 text-[11px] text-emerald-200"
              role="status"
            >
              {{ success }}
            </div>

            <div class="flex justify-end gap-2 pt-2">
              <button
                type="button"
                class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-[11px] text-slate-400 transition hover:bg-white/[0.05]"
                :disabled="saving"
                @click="close"
              >
                取消
              </button>
              <button
                type="submit"
                class="focus-ring rounded-lg bg-amber-400 px-3 py-2 text-[11px] font-semibold text-amber-950 transition hover:bg-amber-300 disabled:opacity-60"
                :disabled="saving"
              >
                {{ saving ? '更新中…' : '更新密碼' }}
              </button>
            </div>
          </form>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
