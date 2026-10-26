<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import {
  ApiError,
  getTraefikLogs,
  getTraefikRoutes,
  getTraefikSettings,
  saveTraefikSettings,
  testTraefik,
} from '../api'
import { getUser, isSuperAdmin } from '../auth'
import type { TraefikLogsResponse, TraefikRoute, TraefikRoutesResponse } from '../types'

const router = useRouter()
const activeTab = ref<'routes' | 'logs' | 'settings'>('routes')
const routes = ref<TraefikRoutesResponse | null>(null)
const logs = ref<TraefikLogsResponse | null>(null)
const loading = ref(true)
const error = ref('')
const routeFilter = ref('')
const logQuery = ref('')
const logStatus = ref('all')
const paused = ref(false)
const lineCount = ref(200)
const settings = ref({ api_url: '', username: '', password: '', log_path: '' })
const passwordConfigured = ref(false)
const saving = ref(false)
const testing = ref(false)
const message = ref('')
let timer: number | undefined

const filteredRoutes = computed<TraefikRoute[]>(() => {
  const needle = routeFilter.value.trim().toLowerCase()
  const rows = routes.value?.routes || []
  if (!needle) return rows
  return rows.filter((route) =>
    [route.name, route.rule, route.service, ...(route.middlewares || [])].join(' ').toLowerCase().includes(needle),
  )
})
const backendCount = computed(() => (routes.value?.routes || []).reduce((sum, route) => sum + route.servers.length, 0))
const viewMode = ref<'topology' | 'cards'>('topology')
const entrypointCount = computed(() => new Set((routes.value?.routes || []).flatMap((route) => route.entry_points)).size)
const middlewareCount = computed(() => new Set((routes.value?.routes || []).flatMap((route) => route.middlewares)).size)
const parsedLogs = computed(() => {
  const rows = logs.value?.lines || []
  return rows
    .map((line) => {
      try {
        const data = JSON.parse(line) as Record<string, unknown>
        return {
          raw: line,
          time: String(data.time || data.StartUTC || ''),
          method: String(data.requestMethod || data.RequestMethod || ''),
          path: String(data.requestPath || data.RequestPath || ''),
          status: Number(data.status || data.Status || 0),
          router: String(data.routerName || data.RouterName || ''),
          duration: Number(data.duration || data.Duration || 0),
        }
      } catch {
        return { raw: line, time: '', method: '', path: '', status: 0, router: '', duration: 0 }
      }
    })
    .filter((row) => {
      if (logStatus.value === '4xx' && !(row.status >= 400 && row.status < 500)) return false
      if (logStatus.value === '5xx' && !(row.status >= 500 && row.status < 600)) return false
      if (logStatus.value === 'error' && row.status < 400) return false
      const needle = logQuery.value.trim().toLowerCase()
      if (needle && !row.raw.toLowerCase().includes(needle)) return false
      return true
    })
})

const logStats = computed(() => {
  const rows = parsedLogs.value
  const total = rows.length
  const errors = rows.filter((row) => row.status >= 400).length
  const serverErrors = rows.filter((row) => row.status >= 500).length
  const avg = total ? Math.round(rows.reduce((sum, row) => sum + row.duration, 0) / total) : 0
  const paths = new Map<string, number>()
  const routers = new Map<string, number>()
  for (const row of rows) {
    if (row.path) paths.set(row.path, (paths.get(row.path) || 0) + 1)
    if (row.router) routers.set(row.router, (routers.get(row.router) || 0) + 1)
  }
  return {
    total,
    errors,
    serverErrors,
    errorRate: total ? ((errors / total) * 100).toFixed(1) : '0.0',
    avg,
    topPaths: [...paths.entries()].sort((a, b) => b[1] - a[1]).slice(0, 5),
    topRouters: [...routers.entries()].sort((a, b) => b[1] - a[1]).slice(0, 5),
  }
})

const topo = computed(() => {
  const columns: { key: string; title: string; color: string; nodes: { id: string; label: string; sub?: string; tone?: string }[] }[] = [
    { key: 'ep', title: 'EntryPoint', color: '#e2c041', nodes: [] },
    { key: 'rt', title: 'Router', color: '#58a6ff', nodes: [] },
    { key: 'mw', title: 'Middleware', color: '#a371f7', nodes: [] },
    { key: 'svc', title: 'Service', color: '#24a1de', nodes: [] },
    { key: 'be', title: 'Backend', color: '#3fb950', nodes: [] },
  ]
  const column = Object.fromEntries(columns.map((item) => [item.key, item])) as Record<string, (typeof columns)[number]>
  const seen = new Set<string>()
  const edges: { from: string; to: string; tone: string }[] = []
  const add = (kind: string, id: string, label: string, sub = '', tone = '') => {
    const key = `${kind}:${id}`
    if (!seen.has(key)) {
      seen.add(key)
      column[kind].nodes.push({ id: key, label, sub, tone })
    }
    return key
  }
  for (const route of filteredRoutes.value) {
    const rt = add('rt', route.name, route.name, route.rule, route.tls ? 'tls' : '')
    const entrypoints = route.entry_points.length ? route.entry_points : ['default']
    for (const entry of entrypoints) {
      edges.push({ from: add('ep', entry, entry), to: rt, tone: '#e2c041' })
    }
    const svc = add('svc', route.service, route.service, `${route.servers.length} backend`, '')
    if (route.middlewares.length === 0) {
      edges.push({ from: rt, to: svc, tone: '#58a6ff' })
    } else {
      const mids = route.middlewares.map((name) => add('mw', name, name))
      for (const mid of mids) edges.push({ from: rt, to: mid, tone: '#a371f7' })
      for (const mid of mids) edges.push({ from: mid, to: svc, tone: '#a371f7' })
    }
    for (const server of route.servers) {
      const be = add('be', server.url, server.url.replace(/^https?:\/\//, ''), server.status || '', server.status === 'DOWN' ? 'down' : 'up')
      edges.push({ from: svc, to: be, tone: '#3fb950' })
    }
  }
  const nodeW = 168
  const nodeH = 42
  const gapX = 64
  const gapY = 12
  const positions = new Map<string, { x: number; y: number; node: (typeof columns)[number]['nodes'][number] }>()
  let maxRows = 1
  columns.forEach((col, colIndex) => {
    maxRows = Math.max(maxRows, col.nodes.length)
    col.nodes.forEach((node, rowIndex) => {
      positions.set(node.id, { x: colIndex * (nodeW + gapX), y: rowIndex * (nodeH + gapY), node })
    })
  })
  const edgeLines = edges
    .map((edge) => {
      const from = positions.get(edge.from)
      const to = positions.get(edge.to)
      if (!from || !to) return null
      const x1 = from.x + nodeW
      const y1 = from.y + 18 + nodeH / 2
      const x2 = to.x
      const y2 = to.y + 18 + nodeH / 2
      const mid = x1 + (x2 - x1) / 2
      return { path: `M ${x1} ${y1} C ${mid} ${y1}, ${mid} ${y2}, ${x2} ${y2}`, tone: edge.tone }
    })
    .filter(Boolean) as { path: string; tone: string }[]
  return {
    columns,
    positions,
    edges: edgeLines,
    nodeW,
    nodeH,
    gapX,
    width: columns.length * (nodeW + gapX),
    height: maxRows * (nodeH + gapY) + 24,
  }
})

const timeline = computed(() => {
  const buckets = new Map<string, { total: number; errors: number }>()
  for (const row of parsedLogs.value) {
    const minute = row.time ? row.time.slice(0, 16) : ''
    if (!minute) continue
    const bucket = buckets.get(minute) || { total: 0, errors: 0 }
    bucket.total += 1
    if (row.status >= 400) bucket.errors += 1
    buckets.set(minute, bucket)
  }
  const entries = [...buckets.entries()].slice(-30)
  const max = Math.max(1, ...entries.map(([, value]) => value.total))
  return entries.map(([minute, value]) => ({
    minute,
    total: value.total,
    errors: value.errors,
    totalHeight: Math.max(2, Math.round((value.total / max) * 100)),
    errorHeight: Math.max(0, Math.round((value.errors / max) * 100)),
  }))
})

function messageOf(value: unknown): string {
  return value instanceof ApiError ? value.message : value instanceof Error ? value.message : String(value)
}

async function loadRoutes(): Promise<void> {
  try {
    routes.value = await getTraefikRoutes()
  } catch (value) {
    error.value = messageOf(value)
  }
}

async function loadLogs(): Promise<void> {
  try {
    logs.value = await getTraefikLogs(lineCount.value)
  } catch (value) {
    error.value = messageOf(value)
  }
}

async function loadSettings(): Promise<void> {
  try {
    const response = await getTraefikSettings()
    settings.value = {
      api_url: response.api_url || '',
      username: response.username || '',
      password: '',
      log_path: response.log_path || '',
    }
    passwordConfigured.value = response.password_configured
  } catch (value) {
    error.value = messageOf(value)
  }
}

async function refresh(): Promise<void> {
  if (paused.value) return
  loading.value = true
  await Promise.all([loadRoutes(), loadLogs()])
  loading.value = false
}

async function saveSettings(): Promise<void> {
  saving.value = true
  error.value = ''
  message.value = ''
  try {
    await saveTraefikSettings(settings.value)
    message.value = 'Traefik settings saved'
    await loadSettings()
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    saving.value = false
  }
}

async function testConnection(): Promise<void> {
  testing.value = true
  error.value = ''
  message.value = ''
  try {
    await testTraefik()
    message.value = 'Traefik API reachable'
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    testing.value = false
  }
}

function statusClass(status: number): string {
  if (status >= 500) return 'text-rose-300'
  if (status >= 400) return 'text-amber-300'
  if (status >= 300) return 'text-sky-300'
  return 'text-emerald-300'
}

function downloadLogs(): void {
  const blob = new Blob([(logs.value?.lines || []).join('\n')], { type: 'text/plain' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `traefik-access-${new Date().toISOString().slice(0, 19).replaceAll(':', '-')}.log`
  link.click()
  URL.revokeObjectURL(url)
}

function startTimer(): void {
  stopTimer()
  timer = window.setInterval(() => {
    if (!paused.value && activeTab.value !== 'settings') void refresh()
  }, 5000)
}

function stopTimer(): void {
  if (timer !== undefined) window.clearInterval(timer)
  timer = undefined
}

watch(activeTab, (tab) => {
  if (tab === 'logs') void loadLogs()
  if (tab === 'routes') void loadRoutes()
  if (tab === 'settings') void loadSettings()
})

onMounted(() => {
  if (!isSuperAdmin(getUser())) {
    void router.replace({ name: 'dashboard' })
    return
  }
  void refresh()
  void loadSettings()
  startTimer()
})
onUnmounted(stopTimer)
</script>

<template>
  <main class="relative mx-auto min-h-screen max-w-7xl px-4 py-8 sm:px-6">
    <header class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <p class="label text-cyan-400/80">Traefik Manager</p>
        <h1 class="mt-1 text-2xl font-semibold tracking-tight text-white">Traefik Manager <span class="text-slate-500">/ 路由管理</span></h1>
        <p class="mt-2 text-xs text-slate-500">Router → Service → Backend route map, live access logs and API settings.</p>
      </div>
      <div class="flex items-center gap-2">
        <button class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-xs text-slate-300 hover:bg-white/[0.05]" @click="router.push({ name: 'dashboard' })">← Dashboard</button>
        <button class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-xs text-slate-300 hover:bg-white/[0.05]" :disabled="loading" @click="refresh">{{ loading ? 'Refreshing…' : 'Refresh' }}</button>
      </div>
    </header>

    <div v-if="error" class="mt-4 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-xs text-rose-200">{{ error }}</div>
    <div v-if="message" class="mt-4 rounded-lg border border-emerald-400/20 bg-emerald-400/[0.06] px-3 py-2 text-xs text-emerald-200">{{ message }}</div>

    <div class="mt-5 flex flex-wrap gap-1 rounded-xl border border-white/[0.06] bg-white/[0.02] p-1">
      <button v-for="tab in [{ id: 'routes', label: 'Route Map / 路由' }, { id: 'logs', label: 'Access Logs / 日誌' }, { id: 'settings', label: 'Settings / 設定' }]" :key="tab.id" type="button" class="focus-ring rounded-lg px-3.5 py-2 text-[11px] font-medium transition" :class="activeTab === tab.id ? 'bg-cyan-400/10 text-cyan-200' : 'text-slate-500 hover:text-slate-300'" @click="activeTab = (tab.id as 'routes' | 'logs' | 'settings')">
        {{ tab.label }}
      </button>
    </div>

    <template v-if="activeTab === 'routes'">
      <section class="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-6">
        <div class="panel p-4"><p class="label">EntryPoints</p><p class="mt-2 font-mono text-2xl text-amber-200">{{ entrypointCount }}</p></div>
        <div class="panel p-4"><p class="label">Routers</p><p class="mt-2 font-mono text-2xl text-white">{{ routes?.routes.length || 0 }}</p></div>
        <div class="panel p-4"><p class="label">Middlewares</p><p class="mt-2 font-mono text-2xl text-violet-300">{{ middlewareCount }}</p></div>
        <div class="panel p-4"><p class="label">Backends</p><p class="mt-2 font-mono text-2xl text-cyan-200">{{ backendCount }}</p></div>
        <div class="panel p-4"><p class="label">TLS</p><p class="mt-2 font-mono text-2xl text-emerald-300">{{ (routes?.routes || []).filter((r) => r.tls).length }}</p></div>
        <div class="panel p-4"><p class="label">Source</p><p class="mt-2 font-mono text-xs" :class="routes?.demo ? 'text-amber-300' : 'text-slate-300'">{{ routes?.demo ? 'DEMO' : routes?.configured ? 'LIVE' : 'NOT CONFIGURED' }}</p></div>
      </section>

      <section class="panel mt-4 overflow-hidden">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-white/[0.06] px-5 py-4">
          <div class="flex items-center gap-3">
            <h2 class="text-sm font-medium text-white">Route Map</h2>
            <div class="flex rounded-lg border border-white/[0.07] bg-black/20 p-0.5">
              <button class="focus-ring rounded px-2 py-1 text-[9px] font-medium" :class="viewMode === 'topology' ? 'bg-cyan-400/10 text-cyan-200' : 'text-slate-500'" @click="viewMode = 'topology'">Topology</button>
              <button class="focus-ring rounded px-2 py-1 text-[9px] font-medium" :class="viewMode === 'cards' ? 'bg-cyan-400/10 text-cyan-200' : 'text-slate-500'" @click="viewMode = 'cards'">Cards</button>
            </div>
          </div>
          <input v-model="routeFilter" placeholder="Filter router / service / middleware" class="focus-ring w-full max-w-xs rounded-lg border border-white/[0.08] bg-black/30 px-3 py-1.5 text-[11px] text-slate-200" />
        </div>
        <div v-if="filteredRoutes.length === 0" class="p-8 text-center text-xs text-slate-500">No routes. Configure the Traefik API in Settings.</div>
        <div v-else-if="viewMode === 'topology'" class="overflow-auto p-4">
          <div class="relative" :style="{ width: `${topo.width}px`, height: `${topo.height}px` }">
            <div class="absolute inset-x-0 top-0 grid grid-cols-5" :style="{ gridTemplateColumns: `repeat(5, ${topo.nodeW}px)`, columnGap: `${topo.gapX}px` }">
              <div v-for="col in topo.columns" :key="col.key" class="label text-center" :style="{ color: col.color }">{{ col.title }}</div>
            </div>
            <svg class="absolute inset-0 h-full w-full overflow-visible" :viewBox="`0 0 ${topo.width} ${topo.height}`" preserveAspectRatio="none" aria-hidden="true">
              <path v-for="(edge, index) in topo.edges" :key="index" :d="edge.path" fill="none" :stroke="edge.tone" stroke-opacity="0.32" stroke-width="1.2" />
            </svg>
            <div
              v-for="[id, position] in topo.positions"
              :key="id"
              class="absolute flex w-[168px] -translate-y-1/2 items-center gap-2 rounded-lg border bg-[#0b1119] px-2.5 py-2 shadow-lg"
              :style="{ left: `${position.x}px`, top: `${position.y + 18 + topo.nodeH / 2}px`, borderColor: `${topo.columns.find((col) => id.startsWith(col.key + ':'))?.color || '#334155'}44` }"
            >
              <span class="h-2 w-2 shrink-0 rounded-full" :class="position.node.tone === 'down' ? 'bg-rose-400' : position.node.tone === 'tls' ? 'bg-emerald-400' : 'bg-cyan-400'"></span>
              <span class="min-w-0">
                <span class="block truncate font-mono text-[10px] text-slate-200" :title="position.node.label">{{ position.node.label }}</span>
                <span v-if="position.node.sub" class="mt-0.5 block truncate font-mono text-[8px] text-slate-500" :title="position.node.sub">{{ position.node.sub }}</span>
              </span>
            </div>
          </div>
        </div>
        <div v-else class="space-y-3 p-4">
          <article v-for="route in filteredRoutes" :key="route.name" class="rounded-xl border border-white/[0.06] bg-white/[0.015] p-4">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div class="min-w-0">
                <p class="font-mono text-xs text-cyan-200">{{ route.name }}</p>
                <p class="mt-1 break-all font-mono text-[11px] text-slate-400">{{ route.rule }}</p>
              </div>
              <div class="flex flex-wrap gap-1.5">
                <span v-for="entry in route.entry_points" :key="entry" class="rounded border border-violet-400/20 bg-violet-400/[0.06] px-1.5 py-0.5 font-mono text-[9px] text-violet-300">{{ entry }}</span>
                <span v-if="route.tls" class="rounded border border-emerald-400/20 bg-emerald-400/[0.06] px-1.5 py-0.5 font-mono text-[9px] text-emerald-300">TLS</span>
                <span class="rounded border px-1.5 py-0.5 font-mono text-[9px]" :class="route.status === 'enabled' ? 'border-emerald-400/20 text-emerald-300' : 'border-amber-400/20 text-amber-300'">{{ route.status }}</span>
              </div>
            </div>
            <div class="mt-3 grid grid-cols-1 items-stretch gap-2 lg:grid-cols-[.8fr_auto_1.2fr_auto_1fr_auto_1fr_auto_1.4fr]">
              <div class="rounded-lg border border-amber-400/15 bg-amber-400/[0.04] p-3">
                <p class="label text-amber-400/70">EntryPoint</p>
                <p v-for="entry in route.entry_points" :key="entry" class="mt-1 truncate font-mono text-[10px] text-slate-200">{{ entry }}</p>
                <p v-if="route.entry_points.length === 0" class="mt-1 text-[10px] text-slate-600">—</p>
              </div>
              <span class="hidden self-center text-slate-600 lg:block">→</span>
              <div class="rounded-lg border border-cyan-400/15 bg-cyan-400/[0.04] p-3">
                <p class="label text-cyan-400/70">Router</p>
                <p class="mt-1 truncate font-mono text-[11px] text-slate-200">{{ route.name }}</p>
                <p class="mt-1 break-all font-mono text-[9px] text-slate-500">{{ route.rule }}</p>
              </div>
              <span class="hidden self-center text-slate-600 lg:block">→</span>
              <div class="rounded-lg border border-violet-400/15 bg-violet-400/[0.04] p-3">
                <p class="label text-violet-400/70">Middlewares ({{ route.middlewares.length }})</p>
                <p v-for="middleware in route.middlewares" :key="middleware" class="mt-1 truncate font-mono text-[9px] text-violet-200" :title="middleware">{{ middleware }}</p>
                <p v-if="route.middlewares.length === 0" class="mt-1 text-[10px] text-slate-600">—</p>
              </div>
              <span class="hidden self-center text-slate-600 lg:block">→</span>
              <div class="rounded-lg border border-sky-400/15 bg-sky-400/[0.04] p-3">
                <p class="label text-sky-400/70">Service</p>
                <p class="mt-1 truncate font-mono text-[11px] text-slate-200">{{ route.service }}</p>
              </div>
              <span class="hidden self-center text-slate-600 lg:block">→</span>
              <div class="min-w-0 rounded-lg border border-emerald-400/15 bg-emerald-400/[0.04] p-3">
                <p class="label text-emerald-400/70">Backend ({{ route.servers.length }})</p>
                <p v-if="route.servers.length === 0" class="mt-1 text-[10px] text-slate-600">no load balancer servers</p>
                <div v-for="server in route.servers" :key="server.url" class="mt-1 flex items-center gap-1.5">
                  <span class="h-1.5 w-1.5 shrink-0 rounded-full" :class="server.status === 'DOWN' ? 'bg-rose-400' : 'bg-emerald-400'"></span>
                  <span class="truncate font-mono text-[10px] text-slate-300" :title="server.url">{{ server.url }}</span>
                </div>
              </div>
            </div>
          </article>
        </div>
      </section>
    </template>

    <template v-if="activeTab === 'logs'">
      <section class="panel mt-4 overflow-hidden">
        <div class="flex flex-wrap items-center justify-between gap-2 border-b border-white/[0.06] px-4 py-3">
          <div class="flex flex-wrap items-center gap-2">
            <input v-model="logQuery" placeholder="Filter path / router / status" class="focus-ring w-56 rounded-lg border border-white/[0.08] bg-black/30 px-2.5 py-1.5 font-mono text-[10px] text-slate-200" />
            <select v-model="logStatus" class="focus-ring rounded-lg border border-white/[0.08] bg-[#0b1119] px-2 py-1.5 text-[10px] text-slate-300">
              <option value="all">All status</option><option value="4xx">4xx</option><option value="5xx">5xx</option><option value="error">4xx + 5xx</option>
            </select>
            <select v-model.number="lineCount" class="focus-ring rounded-lg border border-white/[0.08] bg-[#0b1119] px-2 py-1.5 text-[10px] text-slate-300" @change="loadLogs">
              <option :value="100">100</option><option :value="200">200</option><option :value="500">500</option><option :value="1000">1000</option>
            </select>
          </div>
          <div class="flex items-center gap-2">
            <button class="focus-ring rounded border border-white/[0.08] px-2.5 py-1 text-[10px] text-slate-300" @click="paused = !paused">{{ paused ? 'Resume' : 'Pause' }}</button>
            <button class="focus-ring rounded border border-white/[0.08] px-2.5 py-1 text-[10px] text-slate-300" @click="downloadLogs">Download</button>
            <span class="font-mono text-[9px] text-slate-600">{{ logs?.demo ? 'DEMO' : logs?.source || 'NO LOG SOURCE' }}</span>
          </div>
        </div>
        <div class="grid grid-cols-2 gap-2 border-b border-white/[0.06] p-4 sm:grid-cols-5">
          <div class="rounded-lg border border-white/[0.06] bg-white/[0.02] p-3"><p class="label">Requests</p><p class="mt-1 font-mono text-xl text-white">{{ logStats.total }}</p></div>
          <div class="rounded-lg border border-rose-400/15 bg-rose-400/[0.04] p-3"><p class="label text-rose-300">4xx + 5xx</p><p class="mt-1 font-mono text-xl text-rose-300">{{ logStats.errors }}</p></div>
          <div class="rounded-lg border border-amber-400/15 bg-amber-400/[0.04] p-3"><p class="label text-amber-300">Error rate</p><p class="mt-1 font-mono text-xl text-amber-300">{{ logStats.errorRate }}%</p></div>
          <div class="rounded-lg border border-cyan-400/15 bg-cyan-400/[0.04] p-3"><p class="label text-cyan-300">Avg duration</p><p class="mt-1 font-mono text-xl text-cyan-300">{{ logStats.avg }}ms</p></div>
          <div class="rounded-lg border border-white/[0.06] bg-white/[0.02] p-3"><p class="label">Top router</p><p class="mt-1 truncate font-mono text-[10px] text-slate-300" :title="logStats.topRouters[0]?.[0]">{{ logStats.topRouters[0]?.[0] || '—' }}</p></div>
        </div>
        <div class="grid grid-cols-1 gap-2 border-b border-white/[0.06] p-4 sm:grid-cols-2">
          <div>
            <p class="label mb-1">Top paths</p>
            <div v-for="[path, count] in logStats.topPaths" :key="path" class="flex items-center justify-between gap-2 border-b border-white/[0.03] py-1 font-mono text-[9px]">
              <span class="truncate text-slate-400">{{ path }}</span><span class="text-cyan-300">{{ count }}</span>
            </div>
          </div>
          <div>
            <p class="label mb-1">Top routers</p>
            <div v-for="[name, count] in logStats.topRouters" :key="name" class="flex items-center justify-between gap-2 border-b border-white/[0.03] py-1 font-mono text-[9px]">
              <span class="truncate text-slate-400">{{ name }}</span><span class="text-violet-300">{{ count }}</span>
            </div>
          </div>
        </div>
        <div v-if="timeline.length" class="border-b border-white/[0.06] p-4">
          <div class="flex items-center justify-between"><p class="label">Requests per minute</p><span class="font-mono text-[9px] text-slate-600">last {{ timeline.length }} minutes</span></div>
          <div class="mt-2 flex h-20 items-end gap-1">
            <div v-for="bucket in timeline" :key="bucket.minute" class="group relative flex h-full flex-1 flex-col justify-end" :title="`${bucket.minute} · ${bucket.total} requests · ${bucket.errors} errors`">
              <div class="w-full rounded-t bg-cyan-400/50" :style="{ height: `${bucket.totalHeight}%` }"></div>
              <div v-if="bucket.errors" class="absolute bottom-0 w-full rounded-t bg-rose-400/80" :style="{ height: `${bucket.errorHeight}%` }"></div>
            </div>
          </div>
        </div>
        <div class="max-h-[62vh] overflow-y-auto bg-[#05070c] p-4 font-mono text-[10px] leading-6">
          <div v-if="parsedLogs.length === 0" class="text-slate-600">No log lines. Set access log path in Settings.</div>
          <div v-for="(line, index) in parsedLogs" :key="index" class="flex gap-3 border-b border-white/[0.025] py-1">
            <span class="w-36 shrink-0 truncate text-slate-600">{{ line.time }}</span>
            <span class="w-14 shrink-0 font-semibold" :class="statusClass(line.status)">{{ line.status || '---' }}</span>
            <span class="w-14 shrink-0 text-sky-300">{{ line.method }}</span>
            <span class="min-w-0 flex-1 truncate text-slate-300" :title="line.raw">{{ line.path || line.raw }}</span>
            <span class="hidden w-44 shrink-0 truncate text-violet-300/70 sm:block">{{ line.router }}</span>
          </div>
        </div>
      </section>
    </template>

    <template v-if="activeTab === 'settings'">
      <section class="panel mt-4 p-5">
        <h2 class="text-sm font-medium text-white">Traefik API / Logs</h2>
        <p class="mt-1 text-[10px] leading-5 text-slate-500">
          API URL 例如 http://127.0.0.1:8081。Access log 必須是本 Server 可讀取的路徑；Traefik API 不提供 access log。
        </p>
        <div class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label class="sm:col-span-2"><span class="label">Traefik API URL</span><input v-model="settings.api_url" placeholder="http://127.0.0.1:8081" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200" /></label>
          <label><span class="label">Username</span><input v-model="settings.username" autocomplete="off" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200" /></label>
          <label><span class="label">Password {{ passwordConfigured ? '(configured, leave blank to keep)' : '' }}</span><input v-model="settings.password" type="password" autocomplete="new-password" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200" /></label>
          <label class="sm:col-span-2"><span class="label">Access Log Path (Server local)</span><input v-model="settings.log_path" placeholder="/var/log/traefik/access.log" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200" /></label>
        </div>
        <div class="mt-4 flex flex-wrap gap-2">
          <button class="focus-ring rounded-lg bg-cyan-400 px-4 py-2 text-xs font-semibold text-cyan-950 disabled:opacity-40" :disabled="saving" @click="saveSettings">{{ saving ? 'Saving…' : 'Save Settings' }}</button>
          <button class="focus-ring rounded-lg border border-white/[0.1] px-4 py-2 text-xs text-slate-300 disabled:opacity-40" :disabled="testing" @click="testConnection">{{ testing ? 'Testing…' : 'Test API' }}</button>
        </div>
      </section>
    </template>
  </main>
</template>
