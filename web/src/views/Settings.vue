<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  ApiError,
  disableTOTP,
  downloadBackup,
  enableTOTP,
  getNotificationSettings,
  getTOTPStatus,
  listCloudCredentials,
  saveNotificationSettings,
  testNotification,
  saveCloudCredential,
  setupTOTP,
  testCloudCredential,
} from '../api'
import { getUser, isSuperAdmin } from '../auth'
import { useUIMode } from '../uiMode'

const router = useRouter()
const { isPro } = useUIMode()
const credentials = ref<{ provider: string; masked_key: string }[]>([])
const provider = ref('vultr')
const apiKey = ref('')
const saving = ref(false)
const testing = ref(false)
const message = ref('')
const error = ref('')
const totpEnabled = ref(false)
const totpSecret = ref('')
const totpURL = ref('')
const totpCode = ref('')
const totpPassword = ref('')
const totpSaving = ref(false)
const telegramToken = ref('')
const telegramChatID = ref('')
const webhookURL = ref('')
const notificationConfigured = ref(false)
const notificationSaving = ref(false)
const notificationTesting = ref(false)

function messageOf(value: unknown): string {
  return value instanceof ApiError ? value.message : value instanceof Error ? value.message : String(value)
}

async function load(): Promise<void> {
  try {
    credentials.value = (await listCloudCredentials()).credentials
  } catch (value) {
    error.value = messageOf(value)
  }
}

async function save(): Promise<void> {
  if (!apiKey.value.trim()) return
  saving.value = true
  error.value = ''
  message.value = ''
  try {
    await saveCloudCredential(provider.value, apiKey.value.trim())
    apiKey.value = ''
    message.value = 'Provider 憑證已儲存'
    await load()
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    saving.value = false
  }
}

async function test(): Promise<void> {
  testing.value = true
  error.value = ''
  message.value = ''
  try {
    await testCloudCredential(provider.value, apiKey.value.trim())
    message.value = '連通性測試成功'
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    testing.value = false
  }
}

async function loadNotifications(): Promise<void> {
  try {
    const response = await getNotificationSettings()
    telegramChatID.value = response.telegram_chat_id || ''
    notificationConfigured.value = response.telegram_configured || response.webhook_configured
  } catch {
    // Best-effort.
  }
}

async function saveNotifications(): Promise<void> {
  notificationSaving.value = true
  error.value = ''
  try {
    await saveNotificationSettings({
      telegram_bot_token: telegramToken.value.trim(),
      telegram_chat_id: telegramChatID.value.trim(),
      webhook_url: webhookURL.value.trim(),
    })
    telegramToken.value = ''
    message.value = '通知設定已儲存'
    await loadNotifications()
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    notificationSaving.value = false
  }
}

async function sendTestNotification(): Promise<void> {
  notificationTesting.value = true
  error.value = ''
  try {
    await testNotification()
    message.value = '測試通知已送出'
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    notificationTesting.value = false
  }
}

async function loadTOTP(): Promise<void> {
  try {
    totpEnabled.value = (await getTOTPStatus()).enabled
  } catch {
    // Status is best-effort.
  }
}

async function beginTOTP(): Promise<void> {
  totpSaving.value = true
  error.value = ''
  try {
    const response = await setupTOTP()
    totpSecret.value = response.secret
    totpURL.value = response.otpauth_url
    message.value = '請用 Authenticator 掃描/輸入密鑰，再輸入 6 位碼啟用'
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    totpSaving.value = false
  }
}

async function confirmTOTP(): Promise<void> {
  totpSaving.value = true
  error.value = ''
  try {
    await enableTOTP(totpCode.value.trim())
    totpEnabled.value = true
    totpSecret.value = ''
    totpURL.value = ''
    totpCode.value = ''
    message.value = '兩步驟驗證已啟用'
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    totpSaving.value = false
  }
}

async function removeTOTP(): Promise<void> {
  totpSaving.value = true
  error.value = ''
  try {
    await disableTOTP(totpPassword.value)
    totpEnabled.value = false
    totpPassword.value = ''
    message.value = '兩步驟驗證已停用'
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    totpSaving.value = false
  }
}

async function backup(): Promise<void> {
  try {
    const blob = await downloadBackup()
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `nodehub-backup-${new Date().toISOString().slice(0, 19).replaceAll(':', '-')}.db`
    link.click()
    URL.revokeObjectURL(url)
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
  void loadTOTP()
  void loadNotifications()
})
</script>

<template>
  <main class="relative mx-auto min-h-screen max-w-5xl px-4 py-8 sm:px-6">
    <header class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <p class="label text-cyan-400/80">System Settings</p>
        <h1 class="mt-1 text-2xl font-semibold tracking-tight text-white">系統設定</h1>
        <p class="mt-2 text-xs text-slate-500">集中管理 Provider、API Keys、憑證、使用者與備份。</p>
      </div>
      <button class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-xs text-slate-300 hover:bg-white/[0.05]" @click="router.push({ name: 'dashboard' })">返回 Dashboard</button>
    </header>

    <div v-if="error" class="mt-4 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-xs text-rose-200">{{ error }}</div>
    <div v-if="message" class="mt-4 rounded-lg border border-emerald-400/20 bg-emerald-400/[0.06] px-3 py-2 text-xs text-emerald-200">{{ message }}</div>

    <section class="mt-5 grid grid-cols-2 gap-3 sm:grid-cols-3">
      <button class="panel p-4 text-left transition hover:border-cyan-400/20" @click="router.push({ name: 'users' })"><p class="text-sm font-medium text-white">使用者與訂閱</p><p class="mt-1 text-[10px] text-slate-500">流量 / 配額 / 封禁</p></button>
      <button class="panel p-4 text-left transition hover:border-violet-400/20" @click="router.push({ name: 'api-keys' })"><p class="text-sm font-medium text-white">API Keys</p><p class="mt-1 text-[10px] text-slate-500">商家 Bearer Token</p></button>
      <button class="panel p-4 text-left transition hover:border-emerald-400/20" @click="router.push({ name: 'certificates' })"><p class="text-sm font-medium text-white">證書與偽裝站</p><p class="mt-1 text-[10px] text-slate-500">Wildcard / 模板</p></button>
      <button class="panel p-4 text-left transition hover:border-amber-400/20" @click="router.push({ name: 'audit' })"><p class="text-sm font-medium text-white">操作日誌</p><p class="mt-1 text-[10px] text-slate-500">Audit Trail</p></button>
      <button class="panel p-4 text-left transition hover:border-violet-400/20" @click="router.push({ name: 'admin-users' })"><p class="text-sm font-medium text-white">管理員與 RBAC</p><p class="mt-1 text-[10px] text-slate-500">角色 / AllowedTags</p></button>
      <a class="panel p-4 text-left transition hover:border-cyan-400/20" href="/v1/openapi.json" target="_blank" rel="noopener"><p class="text-sm font-medium text-white">開發者 API</p><p class="mt-1 text-[10px] text-slate-500">OpenAPI 3.0.3</p></a>
    </section>

    <section class="panel mt-4 p-5">
      <h2 class="text-sm font-medium text-white">雲端 Provider 憑證</h2>
      <div class="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-[.6fr_1.4fr]">
        <select v-model="provider" class="focus-ring rounded-lg border border-white/[0.08] bg-[#0b1119] px-3 py-2 text-xs text-slate-200"><option value="vultr">Vultr</option><option value="digitalocean">DigitalOcean</option></select>
        <input v-model="apiKey" type="password" autocomplete="new-password" placeholder="API Key / Token" class="focus-ring rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200" />
      </div>
      <div class="mt-2 flex flex-wrap items-center gap-2">
        <button class="focus-ring rounded-lg border border-white/[0.1] px-3 py-1.5 text-[10px] text-slate-300 disabled:opacity-40" :disabled="testing" @click="test">{{ testing ? '測試中…' : '測試連通性' }}</button>
        <button class="focus-ring rounded-lg bg-violet-400 px-3 py-1.5 text-[10px] font-semibold text-violet-950 disabled:opacity-40" :disabled="saving" @click="save">{{ saving ? '儲存中…' : '儲存 Key' }}</button>
        <span v-for="credential in credentials" :key="credential.provider" class="font-mono text-[9px] text-slate-500">{{ credential.provider }}: {{ credential.masked_key }}</span>
      </div>
    </section>

    <section class="panel mt-4 p-5">
      <div class="flex items-center justify-between gap-3">
        <div>
          <h2 class="text-sm font-medium text-white">兩步驟驗證（TOTP）</h2>
          <p class="mt-1 text-[10px] text-slate-500">支援 Google Authenticator / 1Password / Aegis。</p>
        </div>
        <span class="rounded border px-2 py-0.5 font-mono text-[9px]" :class="totpEnabled ? 'border-emerald-400/20 text-emerald-300' : 'border-slate-500/20 text-slate-500'">{{ totpEnabled ? 'ENABLED' : 'DISABLED' }}</span>
      </div>

      <div v-if="totpEnabled" class="mt-3 flex flex-wrap items-end gap-2">
        <label class="min-w-[220px] flex-1"><span class="label">確認密碼以停用</span><input v-model="totpPassword" type="password" autocomplete="current-password" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 text-xs text-slate-200" /></label>
        <button class="focus-ring rounded-lg border border-rose-400/20 px-3 py-2 text-[10px] text-rose-300 disabled:opacity-40" :disabled="totpSaving" @click="removeTOTP">停用 2FA</button>
      </div>

      <div v-else class="mt-3">
        <button v-if="!totpSecret" class="focus-ring rounded-lg bg-cyan-400 px-3.5 py-2 text-[10px] font-semibold text-cyan-950 disabled:opacity-40" :disabled="totpSaving" @click="beginTOTP">開始設定 2FA</button>
        <div v-else class="space-y-3">
          <div class="rounded-lg border border-cyan-400/15 bg-cyan-400/[0.04] p-3">
            <p class="text-[10px] text-slate-400">密鑰（手動輸入 Authenticator）：</p>
            <code class="mt-1 block break-all font-mono text-xs text-cyan-200">{{ totpSecret }}</code>
            <a :href="totpURL" class="mt-2 block break-all text-[9px] text-slate-500 underline">otpauth URL</a>
          </div>
          <div class="flex flex-wrap items-end gap-2">
            <label class="min-w-[180px] flex-1"><span class="label">6 位驗證碼</span><input v-model="totpCode" inputmode="numeric" maxlength="6" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 text-center font-mono text-sm tracking-[0.3em] text-cyan-100" /></label>
            <button class="focus-ring rounded-lg bg-emerald-400 px-3 py-2 text-[10px] font-semibold text-emerald-950 disabled:opacity-40" :disabled="totpSaving" @click="confirmTOTP">啟用 2FA</button>
          </div>
        </div>
      </div>
    </section>

    <section class="panel mt-4 p-5">
      <div class="flex items-center justify-between gap-3">
        <div>
          <h2 class="text-sm font-medium text-white">Telegram / Webhook 告警</h2>
          <p class="mt-1 text-[10px] text-slate-500">高危電路阻斷、GFW 封鎖與 Auto-Heal 事件會送到這裡。</p>
        </div>
        <span class="rounded border px-2 py-0.5 font-mono text-[9px]" :class="notificationConfigured ? 'border-emerald-400/20 text-emerald-300' : 'border-slate-500/20 text-slate-500'">{{ notificationConfigured ? 'CONFIGURED' : 'EMPTY' }}</span>
      </div>
      <div class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label><span class="label">Telegram Bot Token</span><input v-model="telegramToken" type="password" autocomplete="new-password" placeholder="留空表示不變更" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200" /></label>
        <label><span class="label">Telegram Chat ID</span><input v-model="telegramChatID" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200" /></label>
        <label class="sm:col-span-2"><span class="label">Webhook URL（Discord / Slack / 通用）</span><input v-model="webhookURL" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200" /></label>
      </div>
      <div class="mt-3 flex flex-wrap gap-2">
        <button class="focus-ring rounded-lg bg-cyan-400 px-3 py-2 text-[10px] font-semibold text-cyan-950 disabled:opacity-40" :disabled="notificationSaving" @click="saveNotifications">{{ notificationSaving ? '儲存中…' : '儲存通知設定' }}</button>
        <button class="focus-ring rounded-lg border border-white/[0.1] px-3 py-2 text-[10px] text-slate-300 disabled:opacity-40" :disabled="notificationTesting" @click="sendTestNotification">{{ notificationTesting ? '送出中…' : '送出測試通知' }}</button>
      </div>
    </section>

    <section class="panel mt-4 p-5">
      <h2 class="text-sm font-medium text-white">備份</h2>
      <p class="mt-1 text-[10px] text-slate-500">下載 SQLite snapshot；正式環境請加密保存。</p>
      <button class="focus-ring mt-3 rounded-lg bg-emerald-400 px-4 py-2 text-xs font-semibold text-emerald-950" @click="backup">下載 SQLite 備份</button>
    </section>
  </main>
</template>
