<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import type { DeployRequest, DeployResponse } from '../types'

const props = defineProps<{
  open: boolean
  loading: boolean
  error: string
  result: DeployResponse | null
}>()

const emit = defineEmits<{
  close: []
  submit: [payload: DeployRequest]
}>()

const host = ref('')
const port = ref<number | ''>(22)
const authMode = ref<'password' | 'key'>('password')
const password = ref('')
const privateKey = ref('')
const passphrase = ref('')
const showPassword = ref(false)
const showKey = ref(false)
const formError = ref('')
const hostInput = ref<HTMLInputElement | null>(null)
const closeButton = ref<HTMLButtonElement | null>(null)
let previousOverflow = ''

const canClose = computed(() => !props.loading)

function close(): void {
  if (canClose.value) emit('close')
}

function submit(): void {
  if (props.loading || props.result) return
  const address = host.value.trim()
  const portNumber = Number(port.value)
  if (!address) {
    formError.value = '請輸入目標主機 IP 或域名'
    return
  }
  if (!Number.isInteger(portNumber) || portNumber < 1 || portNumber > 65535) {
    formError.value = 'SSH 端口必須介於 1–65535'
    return
  }
  if (authMode.value === 'password' && !password.value) {
    formError.value = '請輸入 Root 密碼'
    return
  }
  if (authMode.value === 'key' && !privateKey.value.trim()) {
    formError.value = '請貼上 SSH 私鑰'
    return
  }
  formError.value = ''
  const payload: DeployRequest =
    authMode.value === 'key'
      ? {
          host: address,
          port: portNumber,
          private_key: privateKey.value,
          passphrase: passphrase.value || undefined,
        }
      : { host: address, port: portNumber, password: password.value }
  // Do not keep credentials in the form while the request is in flight.
  password.value = ''
  privateKey.value = ''
  passphrase.value = ''
  showPassword.value = false
  showKey.value = false
  emit('submit', payload)
}

function onKeydown(event: KeyboardEvent): void {
  if (props.open && event.key === 'Escape') close()
}

watch(
  () => props.open,
  async (open) => {
    if (open) {
      host.value = ''
      port.value = 22
      authMode.value = 'password'
      password.value = ''
      privateKey.value = ''
      passphrase.value = ''
      showPassword.value = false
      showKey.value = false
      formError.value = ''
      previousOverflow = document.body.style.overflow
      document.body.style.overflow = 'hidden'
      await nextTick()
      hostInput.value?.focus()
    } else {
      password.value = ''
      privateKey.value = ''
      passphrase.value = ''
      showPassword.value = false
      showKey.value = false
      document.body.style.overflow = previousOverflow
    }
  },
)

watch(
  () => props.result,
  async (result) => {
    if (result) {
      password.value = ''
      privateKey.value = ''
      passphrase.value = ''
      showPassword.value = false
      showKey.value = false
      await nextTick()
      closeButton.value?.focus()
    }
  },
)

onMounted(() => window.addEventListener('keydown', onKeydown))
onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  password.value = ''
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
        <div class="fixed inset-0 bg-black/75 backdrop-blur-sm" aria-hidden="true" @click="close" />
        <div class="relative flex min-h-full items-center justify-center p-4 sm:p-6">
          <div
            class="panel max-h-[92vh] w-full max-w-lg overflow-y-auto border-white/[0.1] bg-[#0a0f18]"
            role="dialog"
            aria-modal="true"
            aria-label="一鍵部署節點"
          >
            <div class="h-px bg-gradient-to-r from-transparent via-emerald-400/70 to-transparent" />
            <header class="flex items-start justify-between gap-4 border-b border-white/[0.06] px-5 py-4 sm:px-6">
              <div>
                <p class="label text-emerald-400/80">Zero-Touch Provisioning</p>
                <h2 class="mt-1.5 text-base font-medium text-white sm:text-lg">一鍵部署節點</h2>
              </div>
              <button
                type="button"
                class="focus-ring grid h-8 w-8 shrink-0 place-items-center rounded-lg border border-white/[0.07] text-slate-400 transition hover:bg-white/[0.05] hover:text-white disabled:cursor-not-allowed disabled:opacity-40"
                aria-label="關閉視窗"
                :disabled="!canClose"
                @click="close"
              >
                <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                  <path d="m6 6 12 12M18 6 6 18" stroke-linecap="round" />
                </svg>
              </button>
            </header>

            <div class="px-5 py-5 sm:px-6">
              <div
                v-if="result"
                class="rounded-xl border border-emerald-400/20 bg-emerald-400/[0.06] p-5"
                role="status"
              >
                <div class="flex items-start gap-3">
                  <div class="grid h-9 w-9 shrink-0 place-items-center rounded-full border border-emerald-400/25 bg-emerald-400/10">
                    <svg viewBox="0 0 24 24" class="h-4 w-4 text-emerald-300" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                      <path d="m5 12 4.5 4.5L19 7" stroke-linecap="round" stroke-linejoin="round" />
                    </svg>
                  </div>
                  <div class="min-w-0">
                    <p class="text-sm font-medium text-emerald-100">部署指令已完成</p>
                    <p class="mt-1 text-xs leading-5 text-emerald-100/70">
                      Agent 正在啟動並透過 WebSocket 回連；上線後會出現在節點列表。
                    </p>
                  </div>
                </div>
                <dl class="mt-4 grid grid-cols-2 gap-2 border-t border-emerald-400/10 pt-4">
                  <div>
                    <dt class="label">Node ID</dt>
                    <dd class="mt-1 truncate font-mono text-xs text-emerald-200">{{ result.node_id }}</dd>
                  </div>
                  <div>
                    <dt class="label">SSH Round-trip</dt>
                    <dd class="mt-1 font-mono text-xs text-emerald-200">{{ result.elapsed_ms }}ms</dd>
                  </div>
                </dl>
              </div>

              <form v-else class="space-y-4" @submit.prevent="submit">
                <p class="rounded-lg border border-white/[0.06] bg-white/[0.02] px-3.5 py-3 text-[11px] leading-5 text-slate-400">
                  Root 密碼僅用於本次 SSH 連線，連線結束後立即釋放；不會寫入日誌、資料庫或瀏覽器儲存。
                </p>

                <div>
                  <label for="deploy-host" class="label">目標主機 / 域名</label>
                  <input
                    id="deploy-host"
                    ref="hostInput"
                    v-model="host"
                    type="text"
                    autocomplete="off"
                    autocapitalize="off"
                    spellcheck="false"
                    placeholder="node1.example.com 或 192.0.2.10"
                    class="focus-ring mt-2 w-full rounded-lg border border-white/[0.08] bg-black/25 px-3.5 py-2.5 font-mono text-sm text-slate-100 placeholder:text-slate-600 transition focus:border-emerald-400/40 disabled:opacity-50"
                    :disabled="loading"
                  />
                </div>

                <div>
                  <label for="deploy-port" class="label">SSH 端口</label>
                  <input
                    id="deploy-port"
                    v-model.number="port"
                    type="number"
                    min="1"
                    max="65535"
                    inputmode="numeric"
                    class="focus-ring mt-2 w-full rounded-lg border border-white/[0.08] bg-black/25 px-3.5 py-2.5 font-mono text-sm text-slate-100 transition focus:border-emerald-400/40 disabled:opacity-50"
                    :disabled="loading"
                  />
                </div>

                <div class="grid grid-cols-2 gap-1 rounded-xl border border-white/[0.07] bg-white/[0.02] p-1">
                  <button type="button" class="focus-ring rounded-lg px-3 py-2 text-[11px] font-medium transition" :class="authMode === 'password' ? 'bg-emerald-400/[0.12] text-emerald-200' : 'text-slate-500 hover:text-slate-300'" @click="authMode = 'password'">
                    Root 密碼
                  </button>
                  <button type="button" class="focus-ring rounded-lg px-3 py-2 text-[11px] font-medium transition" :class="authMode === 'key' ? 'bg-violet-400/[0.12] text-violet-200' : 'text-slate-500 hover:text-slate-300'" @click="authMode = 'key'">
                    SSH 私鑰
                  </button>
                </div>

                <div v-if="authMode === 'password'">
                  <label for="deploy-password" class="label">Root 密碼</label>
                  <div class="relative mt-2">
                    <input
                      id="deploy-password"
                      v-model="password"
                      :type="showPassword ? 'text' : 'password'"
                      autocomplete="new-password"
                      spellcheck="false"
                      placeholder="僅用於本次 SSH 驗證"
                      class="focus-ring w-full rounded-lg border border-white/[0.08] bg-black/25 px-3.5 py-2.5 pr-11 font-mono text-sm text-slate-100 placeholder:font-sans placeholder:text-slate-600 transition focus:border-emerald-400/40 disabled:opacity-50"
                      :disabled="loading"
                    />
                    <button
                      type="button"
                      class="focus-ring absolute right-1.5 top-1/2 grid h-8 w-8 -translate-y-1/2 place-items-center rounded-md text-slate-500 transition hover:bg-white/[0.05] hover:text-slate-300 disabled:opacity-40"
                      :aria-label="showPassword ? '隱藏密碼' : '顯示密碼'"
                      :disabled="loading"
                      @click="showPassword = !showPassword"
                    >
                      <svg v-if="!showPassword" viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true">
                        <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12Z" />
                        <circle cx="12" cy="12" r="2.8" />
                      </svg>
                      <svg v-else viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true">
                        <path d="m4 4 16 16M9.9 5.8A10.8 10.8 0 0 1 12 5.5c6 0 9.5 6.5 9.5 6.5a17 17 0 0 1-2.5 3.3M6.2 7.8A17.5 17.5 0 0 0 2.5 12S6 18.5 12 18.5a10 10 0 0 0 4.2-.9" stroke-linecap="round" />
                      </svg>
                    </button>
                  </div>
                </div>

                <div v-if="authMode === 'key'" class="space-y-3">
                  <div>
                    <label for="deploy-private-key" class="label">SSH Private Key</label>
                    <textarea
                      id="deploy-private-key"
                      v-model="privateKey"
                      rows="9"
                      spellcheck="false"
                      :class="showKey ? '' : 'text-security-disc'"
                      placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                      class="focus-ring mt-2 w-full resize-y rounded-lg border border-white/[0.08] bg-black/25 p-3 font-mono text-[11px] leading-5 text-slate-100 placeholder:text-slate-600 focus:border-violet-400/40"
                      :disabled="loading"
                    />
                  </div>
                  <div>
                    <label for="deploy-passphrase" class="label">Passphrase（未加密私鑰留空）</label>
                    <input
                      id="deploy-passphrase"
                      v-model="passphrase"
                      :type="showKey ? 'text' : 'password'"
                      autocomplete="new-password"
                      class="focus-ring mt-2 w-full rounded-lg border border-white/[0.08] bg-black/25 px-3.5 py-2.5 font-mono text-sm text-slate-100 focus:border-violet-400/40"
                      :disabled="loading"
                    />
                  </div>
                  <label class="inline-flex items-center gap-2 text-[11px] text-slate-400">
                    <input v-model="showKey" type="checkbox" class="h-3.5 w-3.5 rounded border-white/20 bg-black/30 text-violet-400" />
                    顯示私鑰 / passphrase
                  </label>
                </div>

                <div
                  v-if="formError || error"
                  class="flex items-start gap-2.5 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3.5 py-3"
                  role="alert"
                >
                  <span class="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-rose-400" />
                  <p class="break-words text-xs leading-5 text-rose-200">{{ formError || error }}</p>
                </div>

                <div class="flex items-center justify-end gap-2 border-t border-white/[0.06] pt-4">
                  <button
                    type="button"
                    class="focus-ring rounded-lg border border-white/[0.08] px-3.5 py-2 text-xs font-medium text-slate-400 transition hover:bg-white/[0.04] hover:text-white disabled:opacity-40"
                    :disabled="loading"
                    @click="close"
                  >
                    取消
                  </button>
                  <button
                    type="submit"
                    class="focus-ring inline-flex items-center gap-2 rounded-lg bg-emerald-400 px-3.5 py-2 text-xs font-semibold text-emerald-950 transition hover:bg-emerald-300 disabled:cursor-wait disabled:opacity-60"
                    :disabled="loading"
                  >
                    <svg v-if="loading" viewBox="0 0 24 24" class="h-3.5 w-3.5 animate-spin" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                      <path d="M21 12a9 9 0 1 1-6.2-8.56" stroke-linecap="round" />
                    </svg>
                    <svg v-else viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                      <path d="M12 5v14M5 12h14" stroke-linecap="round" />
                    </svg>
                    {{ loading ? 'SSH 部署中…' : '開始部署' }}
                  </button>
                </div>
              </form>
            </div>

            <footer v-if="result" class="border-t border-white/[0.06] px-5 py-3 text-right sm:px-6">
              <button
                ref="closeButton"
                type="button"
                class="focus-ring rounded-lg bg-white/[0.06] px-3.5 py-2 text-xs font-medium text-slate-200 transition hover:bg-white/[0.1]"
                @click="close"
              >
                完成
              </button>
            </footer>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
