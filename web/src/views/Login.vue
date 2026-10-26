<script setup lang="ts">
import { nextTick, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '../http'
import { http } from '../http'
import { getDemoStatus } from '../api'
import { setSession } from '../auth'
import LanguageToggle from '../components/LanguageToggle.vue'
import type { LoginResponse } from '../types'

const router = useRouter()
const route = useRoute()
const username = ref('admin')
const password = ref('')
const totpCode = ref('')
const twoFactorRequired = ref(false)
const loading = ref(false)
const error = ref('')
const passwordInput = ref<HTMLInputElement | null>(null)
const totpInput = ref<HTMLInputElement | null>(null)

onMounted(() => {
  void getDemoStatus()
    .then((response) => {
      if (response.demo) {
        username.value = 'demo'
        password.value = 'Demo-NodeHub-2026'
      }
    })
    .catch(() => undefined)
  passwordInput.value?.focus()
})

function safeRedirect(): string {
  const raw = route.query.redirect
  if (typeof raw !== 'string') return '/dashboard'
  if (!raw.startsWith('/') || raw.startsWith('//')) return '/dashboard'
  return raw
}

async function submit(): Promise<void> {
  if (loading.value) return
  error.value = ''
  const user = username.value.trim()
  if (!user || !password.value) {
    error.value = '請輸入管理員帳號與密碼'
    return
  }
  loading.value = true
  try {
    const response = await http.post<LoginResponse>('/api/login', {
      username: user,
      password: password.value,
      totp_code: totpCode.value || undefined,
    })
    setSession(response.data.token, response.data.user, response.data.expires_at)
    await router.replace(safeRedirect())
  } catch (value) {
    const message = value instanceof ApiError ? value.message : '登入失敗，請檢查網路連線'
    error.value = message
    if (value instanceof ApiError && value.status === 401 && message.toLowerCase().includes('two-factor')) {
      twoFactorRequired.value = true
      await nextTick()
      totpInput.value?.focus()
    } else {
      twoFactorRequired.value = false
      password.value = ''
      passwordInput.value?.focus()
    }
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <main class="relative flex min-h-screen items-center justify-center overflow-hidden px-5 py-12">
    <div class="login-aurora" aria-hidden="true"></div>
    <div class="login-grid" aria-hidden="true"></div>

    <div class="absolute right-5 top-5 z-10">
      <LanguageToggle />
    </div>

    <div class="relative w-full max-w-[420px] animate-fade-in">
      <div class="mb-8 flex flex-col items-center text-center">
        <div class="grid h-12 w-12 place-items-center rounded-xl border border-emerald-400/20 bg-emerald-400/[0.07] shadow-[0_0_40px_rgba(52,211,153,.12)]">
          <svg viewBox="0 0 24 24" class="h-6 w-6 text-emerald-300" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
            <circle cx="12" cy="5" r="2.2" />
            <circle cx="5" cy="18" r="2.2" />
            <circle cx="19" cy="18" r="2.2" />
            <path d="M10.6 6.7 6.3 16M13.4 6.7l4.3 9.3M7.2 18h9.6" stroke-linecap="round" />
          </svg>
        </div>
        <p class="mt-5 font-mono text-[10px] uppercase tracking-[0.32em] text-slate-500">
          Distributed Operations
        </p>
        <h1 class="mt-2 text-3xl font-semibold tracking-tight text-white">NODEHUB</h1>
        <p class="mt-3 text-sm text-slate-400">控制平面管理員登入</p>
      </div>

      <form
        class="rounded-2xl border border-white/[0.07] bg-[#0c1017]/85 p-6 shadow-[0_30px_80px_rgba(0,0,0,.45)] backdrop-blur-xl sm:p-7"
        @submit.prevent="submit"
      >
        <div class="space-y-4">
          <label class="block">
            <span class="mb-1.5 block font-mono text-[10px] uppercase tracking-[0.16em] text-slate-500">
              管理員帳號
            </span>
            <input
              v-model="username"
              type="text"
              autocomplete="username"
              spellcheck="false"
              class="focus-ring w-full rounded-lg border border-white/[0.08] bg-black/30 px-3.5 py-2.5 text-sm text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-emerald-400/40"
              placeholder="admin"
              :disabled="loading"
            />
          </label>

          <label class="block">
            <span class="mb-1.5 block font-mono text-[10px] uppercase tracking-[0.16em] text-slate-500">
              密碼
            </span>
            <input
              ref="passwordInput"
              v-model="password"
              type="password"
              autocomplete="current-password"
              class="focus-ring w-full rounded-lg border border-white/[0.08] bg-black/30 px-3.5 py-2.5 text-sm text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-emerald-400/40"
              placeholder="••••••••"
              :disabled="loading"
            />
          </label>

          <label v-if="twoFactorRequired" class="block">
            <span class="mb-1.5 block font-mono text-[10px] uppercase tracking-[0.16em] text-cyan-400/80">
              兩步驟驗證碼
            </span>
            <input
              ref="totpInput"
              v-model="totpCode"
              type="text"
              inputmode="numeric"
              maxlength="6"
              autocomplete="one-time-code"
              placeholder="000000"
              class="focus-ring w-full rounded-lg border border-cyan-400/20 bg-black/30 px-3.5 py-2.5 text-center font-mono text-lg tracking-[0.4em] text-cyan-100 outline-none focus:border-cyan-400/50"
              :disabled="loading"
            />
          </label>
        </div>

        <div
          v-if="error"
          class="mt-4 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3.5 py-2.5 text-xs leading-5 text-rose-200"
          role="alert"
        >
          {{ error }}
        </div>

        <button
          type="submit"
          class="focus-ring mt-5 flex w-full items-center justify-center gap-2 rounded-lg bg-emerald-400 px-4 py-2.5 text-sm font-semibold text-emerald-950 transition hover:bg-emerald-300 disabled:cursor-wait disabled:opacity-60"
          :disabled="loading"
        >
          <svg
            v-if="loading"
            viewBox="0 0 24 24"
            class="h-4 w-4 animate-spin"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            aria-hidden="true"
          >
            <path d="M21 12a9 9 0 1 1-6.2-8.56" stroke-linecap="round" />
          </svg>
          {{ loading ? '驗證中…' : '登入控制台' }}
        </button>

        <div class="mt-5 flex items-center justify-between border-t border-white/[0.06] pt-4">
          <span class="font-mono text-[9px] uppercase tracking-[0.14em] text-slate-600">
            JWT / HS256
          </span>
          <span class="flex items-center gap-1.5 font-mono text-[9px] uppercase tracking-[0.14em] text-slate-600">
            <span class="h-1 w-1 rounded-full bg-emerald-400"></span>
            BEARER / API
          </span>
        </div>
      </form>

      <p class="mt-6 text-center text-[10px] leading-5 text-slate-600">
        所有管理 API 均需 Bearer Token。請勿在公共網路使用預設密碼。
      </p>
    </div>
  </main>
</template>
