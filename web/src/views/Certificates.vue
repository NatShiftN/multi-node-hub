<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  ApiError,
  createCertificate,
  deleteCertificate,
  listCertificates,
  listSiteTemplates,
} from '../api'
import { getUser, isSuperAdmin } from '../auth'
import { useUIMode } from '../uiMode'
import type { CertificateView, SiteTemplate } from '../types'

const router = useRouter()
const { isPro } = useUIMode()
const certificates = ref<CertificateView[]>([])
const templates = ref<SiteTemplate[]>([])
const loading = ref(true)
const saving = ref(false)
const error = ref('')
const notice = ref('')
const form = reactive({ domain: '', certPEM: '', keyPEM: '' })

function messageOf(value: unknown): string {
  return value instanceof ApiError ? value.message : value instanceof Error ? value.message : String(value)
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const [certs, siteTemplates] = await Promise.all([listCertificates(), listSiteTemplates()])
    certificates.value = certs.certificates
    templates.value = siteTemplates.templates
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    loading.value = false
  }
}

async function upload(): Promise<void> {
  if (saving.value) return
  saving.value = true
  error.value = ''
  notice.value = ''
  try {
    await createCertificate({
      domain: form.domain.trim().toLowerCase(),
      cert_pem: form.certPEM,
      key_pem: form.keyPEM,
    })
    form.domain = ''
    form.certPEM = ''
    form.keyPEM = ''
    notice.value = '證書已儲存；私鑰只會下發到節點，API 不會回傳。'
    await load()
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    saving.value = false
  }
}

async function remove(cert: CertificateView): Promise<void> {
  if (!window.confirm(`刪除 ${cert.domain} 的證書？`)) return
  try {
    await deleteCertificate(cert.id)
    await load()
  } catch (value) {
    error.value = messageOf(value)
  }
}

onMounted(() => {
  if (!isPro.value || !isSuperAdmin(getUser())) {
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
        <p class="label text-cyan-400/80">TLS / Fake-Site Distribution</p>
        <h1 class="mt-1 text-2xl font-semibold tracking-tight text-white">泛域名證書與偽裝站</h1>
        <p class="mt-2 max-w-2xl text-xs leading-5 text-slate-500">
          上傳 Wildcard 證書，選擇模板後可在節點詳情一鍵分發。私鑰在節點寫入 0600；API 列表不回傳私鑰。
        </p>
      </div>
      <button class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-xs text-slate-300 hover:bg-white/[0.05]" @click="router.push({ name: 'dashboard' })">
        返回 Dashboard
      </button>
    </header>

    <div v-if="error" class="mt-4 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-xs text-rose-200">{{ error }}</div>
    <div v-if="notice" class="mt-4 rounded-lg border border-emerald-400/20 bg-emerald-400/[0.06] px-3 py-2 text-xs text-emerald-200">{{ notice }}</div>

    <section class="panel mt-5 p-5">
      <h2 class="text-sm font-medium text-white">上傳 Wildcard 證書</h2>
      <p class="mt-1 text-[10px] text-slate-500">例如 example.com 或 *.example.com；證書與私鑰必須匹配。</p>
      <div class="mt-4 grid grid-cols-1 gap-3">
        <label class="block">
          <span class="label">Domain</span>
          <input v-model="form.domain" placeholder="*.example.com" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200" />
        </label>
        <label class="block">
          <span class="label">cert.pem</span>
          <textarea v-model="form.certPEM" rows="6" spellcheck="false" placeholder="-----BEGIN CERTIFICATE-----" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 p-3 font-mono text-[10px] leading-5 text-slate-200" />
        </label>
        <label class="block">
          <span class="label">key.pem（0600 on Agent）</span>
          <textarea v-model="form.keyPEM" rows="6" spellcheck="false" placeholder="-----BEGIN PRIVATE KEY-----" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 p-3 font-mono text-[10px] leading-5 text-slate-200" />
        </label>
      </div>
      <div class="mt-4 flex justify-end">
        <button class="focus-ring rounded-lg bg-cyan-400 px-4 py-2 text-xs font-semibold text-cyan-950 disabled:opacity-50" :disabled="saving" @click="upload">
          {{ saving ? '儲存中…' : '儲存證書' }}
        </button>
      </div>
    </section>

    <section class="panel mt-4 overflow-hidden">
      <div class="border-b border-white/[0.06] px-5 py-4">
        <h2 class="text-sm font-medium text-white">已管理的證書</h2>
      </div>
      <div v-if="loading" class="p-6 text-xs text-slate-500">載入中…</div>
      <div v-else-if="certificates.length === 0" class="p-6 text-xs text-slate-500">尚未上傳證書。</div>
      <div v-else class="divide-y divide-white/[0.045]">
        <div v-for="cert in certificates" :key="cert.id" class="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
          <div>
            <p class="font-mono text-sm text-slate-200">{{ cert.domain }}</p>
            <p class="mt-1 text-[10px] text-slate-500">ID {{ cert.id }} · {{ cert.created_at }}</p>
          </div>
          <button class="focus-ring rounded-md border border-rose-400/20 px-2.5 py-1 text-[10px] text-rose-300 hover:bg-rose-400/[0.06]" @click="remove(cert)">刪除</button>
        </div>
      </div>
    </section>

    <section class="panel mt-4 p-5">
      <h2 class="text-sm font-medium text-white">內建偽裝站模板</h2>
      <div class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div v-for="template in templates" :key="template.id" class="rounded-xl border border-white/[0.06] bg-white/[0.02] p-4">
          <p class="text-xs font-medium text-cyan-200">{{ template.name }}</p>
          <p class="mt-1 text-[10px] leading-5 text-slate-500">{{ template.description }}</p>
        </div>
      </div>
    </section>
  </main>
</template>
