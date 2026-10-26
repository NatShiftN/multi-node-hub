<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, createAdminUser, deleteAdminUser, listAdminUsers, updateAdminUser } from '../api'
import { getUser, isSuperAdmin } from '../auth'
import UIModeToggle from '../components/UIModeToggle.vue'
import { useUIMode } from '../uiMode'
import type { AdminRole, AdminUser } from '../types'

const router = useRouter()
const { isPro } = useUIMode()
const users = ref<AdminUser[]>([])
const loading = ref(true)
const saving = ref(false)
const error = ref('')
const notice = ref('')
const editingID = ref<number | null>(null)
const form = reactive({
  username: '',
  password: '',
  role: 'viewer' as AdminRole,
  allowedTags: '',
})

function tags(value: string): string[] {
  return value.split(',').map((item) => item.trim()).filter(Boolean)
}

function messageOf(value: unknown): string {
  return value instanceof ApiError ? value.message : value instanceof Error ? value.message : String(value)
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    users.value = (await listAdminUsers()).users
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    loading.value = false
  }
}

function reset(): void {
  editingID.value = null
  form.username = ''
  form.password = ''
  form.role = 'viewer'
  form.allowedTags = ''
}

function edit(user: AdminUser): void {
  editingID.value = user.id
  form.username = user.username
  form.password = ''
  form.role = user.role
  form.allowedTags = user.allowed_tags.join(', ')
}

async function save(): Promise<void> {
  if (saving.value) return
  saving.value = true
  error.value = ''
  notice.value = ''
  try {
    if (editingID.value === null) {
      await createAdminUser({
        username: form.username.trim(),
        password: form.password,
        role: form.role,
        allowed_tags: tags(form.allowedTags),
      })
      notice.value = '子帳號已建立'
    } else {
      await updateAdminUser(editingID.value, {
        role: form.role,
        allowed_tags: tags(form.allowedTags),
      })
      notice.value = '權限已更新；下一個請求即生效'
    }
    reset()
    await load()
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    saving.value = false
  }
}

async function remove(user: AdminUser): Promise<void> {
  if (!window.confirm(`刪除管理員 ${user.username}？`)) return
  try {
    await deleteAdminUser(user.id)
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
        <p class="label text-violet-400/80">RBAC / Administration</p>
        <h1 class="mt-1 text-2xl font-semibold tracking-tight text-white">管理員與權限</h1>
        <p class="mt-2 text-xs text-slate-500">SuperAdmin 建立 Operator / Viewer；Operator 僅能管理 AllowedTags 內節點。</p>
      </div>
      <div class="flex items-center gap-2">
        <UIModeToggle />
        <button class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-xs text-slate-300 hover:bg-white/[0.05]" @click="router.push({ name: 'dashboard' })">
          返回 Dashboard
        </button>
      </div>
    </header>

    <div v-if="error" class="mt-4 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-xs text-rose-200">{{ error }}</div>
    <div v-if="notice" class="mt-4 rounded-lg border border-emerald-400/20 bg-emerald-400/[0.06] px-3 py-2 text-xs text-emerald-200">{{ notice }}</div>

    <section class="panel mt-5 p-5">
      <div class="flex items-center justify-between">
        <h2 class="text-sm font-medium text-white">{{ editingID === null ? '建立子帳號' : '編輯權限' }}</h2>
        <button v-if="editingID !== null" class="text-[11px] text-slate-500 hover:text-slate-300" @click="reset">取消編輯</button>
      </div>
      <div class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label class="block">
          <span class="label">Username</span>
          <input v-model="form.username" :disabled="editingID !== null" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200 disabled:opacity-50" />
        </label>
        <label class="block">
          <span class="label">Password {{ editingID !== null ? '（不改）' : '' }}</span>
          <input v-model="form.password" type="password" autocomplete="new-password" :disabled="editingID !== null" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200 disabled:opacity-50" />
        </label>
        <label class="block">
          <span class="label">Role</span>
          <select v-model="form.role" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-[#0c1017] px-3 py-2 text-xs text-slate-200">
            <option value="viewer">Viewer — 唯讀</option>
            <option value="operator">Operator — 標籤內管理</option>
            <option value="superadmin">SuperAdmin — 全部權限</option>
          </select>
        </label>
        <label class="block">
          <span class="label">Allowed Tags（逗號分隔）</span>
          <input v-model="form.allowedTags" placeholder="hk, premium" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200" />
        </label>
      </div>
      <div class="mt-4 flex justify-end">
        <button class="focus-ring rounded-lg bg-violet-400 px-4 py-2 text-xs font-semibold text-violet-950 disabled:opacity-50" :disabled="saving" @click="save">
          {{ saving ? '儲存中…' : editingID === null ? '建立帳號' : '儲存權限' }}
        </button>
      </div>
    </section>

    <section class="panel mt-4 overflow-hidden">
      <div class="border-b border-white/[0.06] px-5 py-4">
        <h2 class="text-sm font-medium text-white">管理員帳號</h2>
      </div>
      <div v-if="loading" class="p-6 text-xs text-slate-500">載入中…</div>
      <div v-else class="divide-y divide-white/[0.045]">
        <div v-for="user in users" :key="user.id" class="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
          <div>
            <p class="font-mono text-sm text-slate-200">{{ user.username }}</p>
            <p class="mt-1 text-[10px] text-slate-500">
              <span class="text-violet-300">{{ user.role }}</span>
              · {{ user.allowed_tags.join(', ') || 'no tags' }}
            </p>
          </div>
          <div class="flex gap-2">
            <button class="focus-ring rounded-md border border-white/[0.08] px-2.5 py-1 text-[10px] text-slate-300 hover:bg-white/[0.05]" @click="edit(user)">編輯</button>
            <button class="focus-ring rounded-md border border-rose-400/20 px-2.5 py-1 text-[10px] text-rose-300 hover:bg-rose-400/[0.06]" @click="remove(user)">刪除</button>
          </div>
        </div>
      </div>
    </section>
  </main>
</template>
