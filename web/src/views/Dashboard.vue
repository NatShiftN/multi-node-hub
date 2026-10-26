<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import * as echarts from 'echarts'
import { clearSession, canWrite, getToken, getUser, isSuperAdmin } from '../auth'
import {
  ApiError,
  deployNode,
  executeCommand,
  getDashboardStats,
  getDemoStatus,
  listNodes,
  listTags,
  telemetryEventsURL,
} from '../api'
import BatchDialog from '../components/BatchDialog.vue'
import LanguageToggle from '../components/LanguageToggle.vue'
import UIModeToggle from '../components/UIModeToggle.vue'
import ChangePasswordDialog from '../components/ChangePasswordDialog.vue'
import DeployNodeDialog from '../components/DeployNodeDialog.vue'
import NodeDetailDialog from '../components/NodeDetailDialog.vue'
import SystemInfoDialog from '../components/SystemInfoDialog.vue'
import TagDialog from '../components/TagDialog.vue'
import { useUIMode } from '../uiMode'
import type {
  AgentNode,
  CommandResponse,
  DashboardStats,
  DeployRequest,
  DeployResponse,
  NodeStatus,
  TelemetrySample,
  TelemetrySnapshot,
  TelemetryView,
} from '../types'

const POLL_INTERVAL_MS = 3_000

const router = useRouter()
const route = useRoute()
const currentUser = getUser()
const { isSimple } = useUIMode()
const canManage = computed(() => canWrite(currentUser))
const superAdmin = computed(() => isSuperAdmin(currentUser))
const nodes = ref<AgentNode[]>([])
const demoMode = ref(false)
const stats = ref<DashboardStats | null>(null)
const page = ref(1)
const pageLimit = ref(20)
const total = ref(0)
const totalPages = ref(0)
const search = ref('')
const statusFilter = ref('')
const tagFilter = ref('')
const tags = ref<string[]>([])
let searchTimer: number | undefined
let deepLinkHandled = false
const chartElement = ref<HTMLDivElement | null>(null)
let chart: echarts.ECharts | null = null
const now = ref(Date.now())
const initialLoading = ref(true)
const refreshing = ref(false)
const syncConnected = ref(false)
const syncError = ref('')
const lastSync = ref<Date | null>(null)

const dialogOpen = ref(false)
const selectedNode = ref<AgentNode | null>(null)
const commandLoading = ref(false)
const commandResult = ref<CommandResponse | null>(null)
const commandError = ref('')

const deployOpen = ref(false)
const deployLoading = ref(false)
const deployError = ref('')
const deployResult = ref<DeployResponse | null>(null)

const detailOpen = ref(false)
const detailNodeId = ref<string | null>(null)
const passwordOpen = ref(false)
const batchOpen = ref(false)
const tagOpen = ref(false)
const tagNode = ref<AgentNode | null>(null)
const detailNode = computed(
  () => nodes.value.find((node) => node.id === detailNodeId.value) ?? null,
)

const telemetry = ref<Record<string, TelemetryView>>({})
const previousNet = new Map<string, { rx: number; tx: number; at: number }>()
let telemetryAbort: AbortController | undefined
let telemetryReconnectTimer: number | undefined
let telemetryStopped = false

let pollTimer: number | undefined
let clockTimer: number | undefined
let deployRefreshTimer: number | undefined
let inFlight: AbortController | null = null

const onlineCount = computed(() => stats.value?.nodes_online ?? 0)
const monthlyTraffic = computed(
  () => (stats.value?.monthly_upload_bytes ?? 0) + (stats.value?.monthly_download_bytes ?? 0),
)
const averageLoad = computed(() => stats.value?.average_cpu_percent ?? 0)
const commandBusy = computed(() => commandLoading.value)
const connectionLabel = computed(() => {
  if (syncConnected.value) return 'CONTROL PLANE ONLINE'
  return initialLoading.value ? 'CONNECTING' : 'DISCONNECTED'
})
const dialogTitle = computed(() => {
  const node = selectedNode.value
  return node ? `${node.name || node.id} · system_info` : 'system_info'
})
function messageOf(error: unknown): string {
  if (error instanceof ApiError) return error.message
  if (error instanceof DOMException && error.name === 'AbortError') {
    return '控制平面回應逾時（5 秒）'
  }
  if (error instanceof Error) return error.message
  return String(error)
}

async function refresh(manual = false): Promise<void> {
  if (inFlight) return
  const controller = new AbortController()
  inFlight = controller
  if (manual) refreshing.value = true
  const timeout = window.setTimeout(() => controller.abort(), 8_000)

  try {
    const [data, dashboard] = await Promise.all([
      listNodes({
        page: page.value,
        limit: pageLimit.value,
        search: search.value.trim(),
        status: statusFilter.value,
        tag: tagFilter.value,
        signal: controller.signal,
      }),
      getDashboardStats(),
    ])
    nodes.value = data.nodes
    stats.value = dashboard
    total.value = data.total
    totalPages.value = data.total_pages
    if (data.total_pages > 0 && page.value > data.total_pages) {
      page.value = data.total_pages
    }
    syncConnected.value = true
    syncError.value = ''
    lastSync.value = new Date()
    await renderPie()
    const requestedNode = typeof route.query.node === 'string' ? route.query.node : ''
    if (requestedNode && !deepLinkHandled) {
      deepLinkHandled = true
      const target = data.nodes.find((node) => node.id === requestedNode)
      if (target) openNodeDetails(target)
    }
  } catch (error) {
    syncConnected.value = false
    syncError.value = messageOf(error)
  } finally {
    window.clearTimeout(timeout)
    if (inFlight === controller) inFlight = null
    refreshing.value = false
    initialLoading.value = false
  }
}

function formatBytes(value: number | undefined): string {
  if (!value || !Number.isFinite(value)) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024
    unit += 1
  }
  return `${amount.toFixed(amount >= 100 ? 0 : 1)} ${units[unit]}`
}

function sparklinePoints(history: number[] | undefined): string {
  if (!history || history.length === 0) return ''
  const width = 96
  const height = 24
  const max = Math.max(100, ...history)
  const step = history.length > 1 ? width / (history.length - 1) : width
  return history
    .map((value, index) => {
      const x = index * step
      const y = height - (Math.max(0, Math.min(100, value)) / max) * height
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
}

async function renderPie(): Promise<void> {
  await nextTick()
  if (!chartElement.value) return
  if (!chart) chart = echarts.init(chartElement.value, undefined, { renderer: 'svg' })
  const online = stats.value?.nodes_online ?? 0
  const offline = stats.value?.nodes_offline ?? 0
  chart.setOption({
    animationDuration: 500,
    tooltip: { trigger: 'item' },
    legend: { bottom: 0, textStyle: { color: '#94a3b8', fontSize: 10 } },
    series: [
      {
        type: 'pie',
        radius: ['52%', '76%'],
        center: ['50%', '44%'],
        avoidLabelOverlap: true,
        label: { show: false },
        data: [
          { value: online, name: 'Online', itemStyle: { color: '#34d399' } },
          { value: offline, name: 'Offline', itemStyle: { color: '#475569' } },
        ],
      },
    ],
  })
}

function resizeChart(): void {
  chart?.resize()
}

async function loadTags(): Promise<void> {
  try {
    const response = await listTags()
    tags.value = response.tags.map((item) => item.tag)
  } catch {
    tags.value = []
  }
}

function applyFilters(): void {
  page.value = 1
  void refresh()
}

function scheduleSearch(): void {
  if (searchTimer !== undefined) window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(applyFilters, 300)
}

function goPage(next: number): void {
  const target = Math.max(1, Math.min(totalPages.value || 1, next))
  if (target === page.value) return
  page.value = target
  void refresh()
}

function onlineSeconds(node: AgentNode): number {
  if (node.status !== 'online') return 0
  const started = node.connected_at ? Date.parse(node.connected_at) : Number.NaN
  if (!Number.isFinite(started)) return node.online_seconds
  return Math.max(0, Math.floor((now.value - started) / 1_000))
}

function statusLabel(status: NodeStatus): string {
  switch (status) {
    case 'online':
      return 'Online'
    case 'provisioning':
      return 'Provisioning'
    case 'offline_network_error':
      return 'Network Error'
    case 'gfw_blocked':
      return 'GFW Blocked'
    default:
      return 'Offline'
  }
}

function statusClasses(status: NodeStatus): string {
  switch (status) {
    case 'online':
      return 'border-emerald-400/15 bg-emerald-400/[0.06] text-emerald-300'
    case 'provisioning':
      return 'border-amber-400/15 bg-amber-400/[0.06] text-amber-300'
    case 'offline_network_error':
      return 'border-rose-400/20 bg-rose-400/[0.07] text-rose-300'
    case 'gfw_blocked':
      return 'border-orange-400/25 bg-orange-400/[0.08] text-orange-300'
    default:
      return 'border-slate-500/20 bg-slate-500/[0.06] text-slate-400'
  }
}

function statusDotClasses(status: NodeStatus): string {
  switch (status) {
    case 'online':
      return 'bg-emerald-400'
    case 'provisioning':
      return 'bg-amber-400'
    case 'offline_network_error':
      return 'bg-rose-500'
    case 'gfw_blocked':
      return 'bg-orange-400'
    default:
      return 'bg-slate-500'
  }
}

function lastSeenLabel(node: AgentNode): string {
  if (!node.last_seen) {
    return node.status === 'provisioning' ? '等待回連' : '從未上線'
  }
  const date = new Date(node.last_seen)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat('zh-Hant', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(date)
}

function openNodeDetails(node: AgentNode): void {
  detailNodeId.value = node.id
  detailOpen.value = true
}

function closeNodeDetails(): void {
  detailOpen.value = false
}

function openTagDialog(node: AgentNode): void {
  tagNode.value = node
  tagOpen.value = true
}

function closeTagDialog(): void {
  tagOpen.value = false
  tagNode.value = null
}

function logout(): void {
  stopTelemetryStream()
  clearSession()
  void router.replace({ name: 'login' })
}

function ingestTelemetry(sample: TelemetrySample, receivedAt = performance.now()): void {
  if (!sample.node_id) return
  const cpu = Number(sample.cpu_percent)
  const memory = Number(sample.memory_percent)
  const rx = Number(sample.net_rx_bytes)
  const tx = Number(sample.net_tx_bytes)
  if (![cpu, memory, rx, tx].every(Number.isFinite)) return

  const previous = previousNet.get(sample.node_id)
  const elapsed = previous ? (receivedAt - previous.at) / 1_000 : 0
  let rxRate = 0
  let txRate = 0
  if (previous && elapsed > 0) {
    if (rx >= previous.rx) rxRate = (rx - previous.rx) / elapsed
    if (tx >= previous.tx) txRate = (tx - previous.tx) / elapsed
  }
  previousNet.set(sample.node_id, { rx, tx, at: receivedAt })
  telemetry.value = {
    ...telemetry.value,
    [sample.node_id]: {
      ...sample,
      cpu_percent: Math.max(0, Math.min(100, cpu)),
      memory_percent: Math.max(0, Math.min(100, memory)),
      receivedAt,
      rxBytesPerSecond: rxRate,
      txBytesPerSecond: txRate,
    },
  }
}

function handleTelemetryBlock(block: string): void {
  let event = 'message'
  const data: string[] = []
  for (const line of block.split(/\r?\n/)) {
    if (line.startsWith('event:')) event = line.slice(6).trim()
    else if (line.startsWith('data:')) data.push(line.slice(5).trimStart())
  }
  if (data.length === 0) return
  const payload = data.join('\n')
  try {
    if (event === 'telemetry_snapshot') {
      const snapshot = JSON.parse(payload) as TelemetrySnapshot
      const now = performance.now()
      for (const sample of snapshot.samples) ingestTelemetry(sample, now)
    } else if (event === 'telemetry_update') {
      ingestTelemetry(JSON.parse(payload) as TelemetrySample)
    }
  } catch {
    // Ignore malformed telemetry; the node polling remains functional.
  }
}

function scheduleTelemetryReconnect(): void {
  if (telemetryStopped || telemetryReconnectTimer !== undefined) return
  telemetryReconnectTimer = window.setTimeout(() => {
    telemetryReconnectTimer = undefined
    void openTelemetryStream()
  }, 3_000)
}

function stopTelemetryStream(): void {
  telemetryStopped = true
  if (telemetryReconnectTimer !== undefined) {
    window.clearTimeout(telemetryReconnectTimer)
    telemetryReconnectTimer = undefined
  }
  telemetryAbort?.abort()
  telemetryAbort = undefined
}

function connectTelemetryStream(): void {
  telemetryStopped = false
  void openTelemetryStream()
}

async function openTelemetryStream(): Promise<void> {
  if (telemetryStopped || telemetryAbort) return
  const token = getToken()
  if (!token) {
    clearSession()
    void router.replace({ name: 'login' })
    return
  }
  const controller = new AbortController()
  telemetryAbort = controller
  let reconnect = true
  try {
    const response = await fetch(telemetryEventsURL(), {
      headers: {
        Accept: 'text/event-stream',
        Authorization: `Bearer ${token}`,
        'Cache-Control': 'no-store',
      },
      signal: controller.signal,
      cache: 'no-store',
    })
    if (response.status === 401) {
      reconnect = false
      clearSession()
      void router.replace({ name: 'login' })
      return
    }
    if (!response.ok || !response.body) {
      throw new Error(`telemetry stream HTTP ${response.status}`)
    }
    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    while (!telemetryStopped) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      let boundary = buffer.indexOf('\n\n')
      while (boundary >= 0) {
        handleTelemetryBlock(buffer.slice(0, boundary))
        buffer = buffer.slice(boundary + 2)
        boundary = buffer.indexOf('\n\n')
      }
    }
  } catch {
    // Network/server restarts are retried below. Abort is expected on logout.
  } finally {
    if (telemetryAbort === controller) telemetryAbort = undefined
    if (reconnect && !telemetryStopped) scheduleTelemetryReconnect()
  }
}

function percentWidth(value: number | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '0%'
  return `${Math.max(0, Math.min(100, value))}%`
}

function formatPercent(value: number | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—'
  return `${value.toFixed(0)}%`
}

function formatRate(bytesPerSecond: number | undefined): string {
  if (typeof bytesPerSecond !== 'number' || !Number.isFinite(bytesPerSecond) || bytesPerSecond <= 0) {
    return '0 B/s'
  }
  const units = ['B/s', 'KiB/s', 'MiB/s', 'GiB/s']
  let value = bytesPerSecond
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value.toFixed(value >= 100 ? 0 : 1)} ${units[unit]}`
}

function formatDuration(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds))
  const days = Math.floor(total / 86_400)
  const hours = Math.floor((total % 86_400) / 3_600)
  const minutes = Math.floor((total % 3_600) / 60)
  const secs = total % 60
  const clock = [hours, minutes, secs]
    .map((part) => String(part).padStart(2, '0'))
    .join(':')
  return days > 0 ? `${days}d ${clock}` : clock
}

async function inspect(node: AgentNode): Promise<void> {
  if (commandBusy.value) return
  selectedNode.value = node
  commandResult.value = null
  commandError.value = ''
  commandLoading.value = true
  dialogOpen.value = true

  try {
    commandResult.value = await executeCommand(node.id, 'system_info')
  } catch (error) {
    commandError.value = messageOf(error)
    if (error instanceof ApiError && error.status === 404) {
      void refresh()
    }
  } finally {
    commandLoading.value = false
  }
}

function closeDialog(): void {
  dialogOpen.value = false
}

function openDeployDialog(): void {
  deployError.value = ''
  deployResult.value = null
  deployOpen.value = true
}

function closeDeployDialog(): void {
  if (deployLoading.value) return
  deployOpen.value = false
  deployError.value = ''
  deployResult.value = null
}

async function submitDeploy(payload: DeployRequest): Promise<void> {
  if (deployLoading.value) return
  deployLoading.value = true
  deployError.value = ''
  deployResult.value = null
  try {
    const request = deployNode(payload)
    // The API helper serializes the body before its first await; drop the
    // password reference from the component state immediately afterwards.
    payload.password = ''
    payload.private_key = ''
    payload.passphrase = ''
    deployResult.value = await request
    void refresh()
    if (deployRefreshTimer !== undefined) {
      window.clearTimeout(deployRefreshTimer)
    }
    deployRefreshTimer = window.setTimeout(() => void refresh(), 2_500)
  } catch (error) {
    deployError.value = messageOf(error)
  } finally {
    deployLoading.value = false
  }
}

onMounted(() => {
  void getDemoStatus().then((response) => { demoMode.value = response.demo }).catch(() => undefined)
  void refresh()
  void loadTags()
  connectTelemetryStream()
  pollTimer = window.setInterval(() => void refresh(), POLL_INTERVAL_MS)
  clockTimer = window.setInterval(() => {
    now.value = Date.now()
  }, 1_000)
  window.addEventListener('resize', resizeChart)
})

onUnmounted(() => {
  if (pollTimer !== undefined) window.clearInterval(pollTimer)
  if (clockTimer !== undefined) window.clearInterval(clockTimer)
  if (deployRefreshTimer !== undefined) window.clearTimeout(deployRefreshTimer)
  if (searchTimer !== undefined) window.clearTimeout(searchTimer)
  window.removeEventListener('resize', resizeChart)
  chart?.dispose()
  chart = null
  stopTelemetryStream()
  inFlight?.abort()
})
</script>

<template>
  <div class="relative min-h-screen overflow-hidden">
    <div
      class="pointer-events-none absolute inset-0 opacity-[0.22]"
      style="
        background-image:
          linear-gradient(to right, rgba(148, 163, 184, 0.12) 1px, transparent 1px),
          linear-gradient(to bottom, rgba(148, 163, 184, 0.12) 1px, transparent 1px);
        background-size: 64px 64px;
        mask-image: linear-gradient(to bottom, black, transparent 75%);
      "
      aria-hidden="true"
    />

    <div class="relative mx-auto flex min-h-screen max-w-7xl flex-col px-4 py-6 sm:px-6 lg:px-8">
      <header class="flex flex-wrap items-center justify-between gap-4">
        <div class="flex items-center gap-3">
          <div
            class="grid h-10 w-10 place-items-center rounded-xl border border-emerald-400/20 bg-emerald-400/[0.07]"
          >
            <svg
              viewBox="0 0 24 24"
              class="h-5 w-5 text-emerald-300"
              fill="none"
              stroke="currentColor"
              stroke-width="1.6"
              aria-hidden="true"
            >
              <circle cx="12" cy="5" r="2.2" />
              <circle cx="5" cy="18" r="2.2" />
              <circle cx="19" cy="18" r="2.2" />
              <path d="M10.8 7 6.2 15.8M13.2 7l4.6 8.8M7.2 18h9.6" />
            </svg>
          </div>
          <div>
            <p class="label">Distributed Operations</p>
            <h1 class="text-xl font-semibold tracking-tight text-white">
              NODE<span class="text-emerald-400">HUB</span>
            </h1>
          </div>
        </div>

        <div class="flex items-center gap-2">
          <LanguageToggle />
          <UIModeToggle />
          <div
            class="hidden items-center gap-2 rounded-full border border-white/[0.07] bg-white/[0.025] px-3 py-1.5 sm:flex"
          >
            <span
              class="h-1.5 w-1.5 rounded-full"
              :class="
                syncConnected
                  ? 'animate-pulse-soft bg-emerald-400 shadow-[0_0_8px_rgba(52,211,153,.9)]'
                  : 'bg-rose-500'
              "
            />
            <span class="font-mono text-[10px] tracking-[0.12em] text-slate-400">
              {{ connectionLabel }}
            </span>
          </div>
          <button
            type="button"
            class="focus-ring inline-flex items-center gap-2 rounded-lg border border-white/[0.08] bg-white/[0.035] px-3 py-2 text-xs font-medium text-slate-300 transition hover:border-white/[0.15] hover:bg-white/[0.06] hover:text-white disabled:cursor-wait disabled:opacity-60"
            :disabled="refreshing"
            @click="refresh(true)"
          >
            <svg
              viewBox="0 0 24 24"
              class="h-3.5 w-3.5"
              :class="{ 'animate-spin': refreshing }"
              fill="none"
              stroke="currentColor"
              stroke-width="1.8"
              aria-hidden="true"
            >
              <path d="M20 11a8 8 0 1 0-2.34 5.66M20 4v7h-7" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
            同步
          </button>
          <button
            v-if="!isSimple"
            type="button"
            class="focus-ring inline-flex items-center gap-2 rounded-lg border border-white/[0.08] bg-white/[0.035] px-3 py-2 text-xs font-medium text-slate-400 transition hover:border-amber-400/20 hover:bg-amber-400/[0.06] hover:text-amber-200"
            @click="router.push({ name: 'audit' })"
          >
            <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
              <path d="M5 4h14v16H5zM8 8h8M8 12h6M8 16h4" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
            日誌
          </button>
          <a
            v-if="!isSimple"
            href="/v1/openapi.json"
            target="_blank"
            rel="noopener"
            class="focus-ring inline-flex items-center gap-2 rounded-lg border border-white/[0.08] bg-white/[0.035] px-3 py-2 text-xs font-medium text-slate-400 transition hover:border-cyan-400/20 hover:bg-cyan-400/[0.06] hover:text-cyan-200"
            title="開發者 API / OpenAPI"
          >
            API
          </a>
          <button
            v-if="superAdmin && !isSimple"
            type="button"
            class="focus-ring inline-flex items-center gap-2 rounded-lg border border-white/[0.08] bg-white/[0.035] px-3 py-2 text-xs font-medium text-slate-400 transition hover:border-cyan-400/20 hover:bg-cyan-400/[0.06] hover:text-cyan-200"
            @click="router.push({ name: 'certificates' })"
          >
            憑證
          </button>
          <button
            v-if="superAdmin && !isSimple"
            type="button"
            class="focus-ring inline-flex items-center gap-2 rounded-lg border border-white/[0.08] bg-white/[0.035] px-3 py-2 text-xs font-medium text-slate-400 transition hover:border-cyan-400/20 hover:bg-cyan-400/[0.06] hover:text-cyan-200"
            @click="router.push({ name: 'settings' })"
          >
            系統設定
          </button>
          <button
            v-if="superAdmin && (!isSimple || demoMode)"
            type="button"
            class="focus-ring inline-flex items-center gap-2 rounded-lg border border-white/[0.08] bg-white/[0.035] px-3 py-2 text-xs font-medium text-slate-400 transition hover:border-cyan-400/20 hover:bg-cyan-400/[0.06] hover:text-cyan-200"
            @click="router.push({ name: 'traefik' })"
          >
            Traefik
          </button>
          <button
            v-if="superAdmin && !isSimple"
            type="button"
            class="focus-ring inline-flex items-center gap-2 rounded-lg border border-white/[0.08] bg-white/[0.035] px-3 py-2 text-xs font-medium text-slate-400 transition hover:border-violet-400/20 hover:bg-violet-400/[0.06] hover:text-violet-200"
            @click="router.push({ name: 'admin-users' })"
          >
            <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
              <circle cx="9" cy="8" r="3" />
              <path d="M3 20c0-3.3 2.7-6 6-6s6 2.7 6 6M17 11l2 2 3-3" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
            管理員
          </button>
          <button
            type="button"
            class="focus-ring inline-flex items-center gap-2 rounded-lg border border-white/[0.08] bg-white/[0.035] px-3 py-2 text-xs font-medium text-slate-400 transition hover:border-amber-400/20 hover:bg-amber-400/[0.06] hover:text-amber-200"
            :title="currentUser ? `管理員 ${currentUser.username} · ${currentUser.role}` : '管理員帳號'"
            @click="passwordOpen = true"
          >
            <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
              <circle cx="8" cy="12" r="3.2" />
              <path d="M11.2 12H21m-3 0v3m-3-3v2" stroke-linecap="round" />
            </svg>
            帳號
          </button>
          <button
            type="button"
            class="focus-ring inline-flex items-center gap-2 rounded-lg border border-white/[0.08] bg-white/[0.035] px-3 py-2 text-xs font-medium text-slate-400 transition hover:border-rose-400/20 hover:bg-rose-400/[0.06] hover:text-rose-200"
            :title="currentUser ? `登出 ${currentUser.username}` : '登出'"
            @click="logout"
          >
            <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
              <path d="M15 8l4 4-4 4M19 12H9M12 4H5v16h7" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
            登出
          </button>
        </div>
      </header>

      <section class="mt-10 flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="label text-emerald-400/80">Node Topology</p>
          <h2 class="mt-2 text-2xl font-semibold tracking-tight text-white sm:text-3xl">
            多節點管理中樞
          </h2>
        </div>
        <div class="flex items-center gap-2 font-mono text-[10px] uppercase tracking-[0.16em] text-slate-500">
          <span class="h-px w-8 bg-gradient-to-r from-transparent to-emerald-400/60" />
          Edge / Plugin Architecture
        </div>
      </section>

      <section class="mt-6 grid grid-cols-1 gap-4 lg:grid-cols-12">
        <div class="panel relative flex min-h-[300px] flex-col p-5 lg:col-span-4">
          <div class="flex items-start justify-between">
            <div>
              <p class="label">Fleet Reachability</p>
              <p class="mt-1 font-mono text-[10px] text-slate-500">ONLINE / OFFLINE</p>
            </div>
            <span class="rounded border border-white/[0.07] px-2 py-1 font-mono text-[10px] text-slate-400">
              {{ stats?.nodes_total ?? 0 }} NODES
            </span>
          </div>
          <div ref="chartElement" class="mt-2 h-[230px] w-full" />
          <div class="pointer-events-none absolute inset-x-0 top-[46%] text-center">
            <p class="font-mono text-3xl font-semibold text-white">{{ onlineCount }}</p>
            <p class="font-mono text-[9px] uppercase tracking-[0.18em] text-emerald-300/80">online</p>
          </div>
        </div>

        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:col-span-8">
          <div class="panel p-5">
            <div class="flex items-center justify-between">
              <p class="label">Monthly Traffic</p>
              <span class="font-mono text-[9px] text-emerald-300/70">UP + DOWN</span>
            </div>
            <p class="mt-3 font-mono text-3xl font-medium tabular-nums text-white">{{ formatBytes(monthlyTraffic) }}</p>
            <div class="mt-3 grid grid-cols-2 gap-3 border-t border-white/[0.06] pt-3 font-mono text-[10px]">
              <span class="text-sky-300">UP {{ formatBytes(stats?.monthly_upload_bytes) }}</span>
              <span class="text-emerald-300">DOWN {{ formatBytes(stats?.monthly_download_bytes) }}</span>
            </div>
          </div>
          <div v-if="!isSimple" class="panel p-5">
            <p class="label">Banned Users</p>
            <p class="mt-3 font-mono text-3xl font-medium tabular-nums text-rose-300">{{ stats?.banned_users ?? 0 }}</p>
            <p class="mt-3 border-t border-white/[0.06] pt-3 font-mono text-[10px] text-slate-500">QUOTA / MANUAL ENFORCEMENT</p>
          </div>
          <div class="panel p-5">
            <p class="label">Average Load</p>
            <p class="mt-3 font-mono text-3xl font-medium tabular-nums text-cyan-200">{{ averageLoad.toFixed(1) }}%</p>
            <div class="mt-3 h-1.5 overflow-hidden rounded-full bg-white/[0.06]">
              <div class="h-full rounded-full bg-gradient-to-r from-cyan-500 to-emerald-300 transition-all" :style="{ width: percentWidth(averageLoad) }" />
            </div>
          </div>
          <div v-if="!isSimple" class="panel p-5">
            <p class="label">GFW / Telemetry</p>
            <div class="mt-3 flex items-end justify-between">
              <p class="font-mono text-3xl font-medium tabular-nums text-orange-300">{{ stats?.nodes_gfw_blocked ?? 0 }}</p>
              <p class="font-mono text-[10px] text-slate-500">{{ stats?.telemetry_nodes ?? 0 }} SAMPLES</p>
            </div>
            <p class="mt-3 border-t border-white/[0.06] pt-3 font-mono text-[10px] text-slate-500">AVG RAM {{ (stats?.average_memory_percent ?? 0).toFixed(1) }}%</p>
          </div>
        </div>
      </section>

      <div
        v-if="syncError"
        class="mt-4 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-rose-500/20 bg-rose-500/[0.06] px-4 py-3"
        role="alert"
      >
        <div class="flex items-center gap-2.5">
          <span class="h-1.5 w-1.5 rounded-full bg-rose-400" />
          <p class="text-xs text-rose-200">
            <span class="font-semibold">同步失敗：</span>{{ syncError }}
          </p>
        </div>
        <button
          type="button"
          class="focus-ring rounded-md border border-rose-400/25 px-2.5 py-1 text-[11px] font-medium text-rose-200 transition hover:bg-rose-400/10"
          @click="refresh(true)"
        >
          重試
        </button>
      </div>

      <div v-if="demoMode" class="mt-4 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-amber-400/20 bg-amber-400/[0.06] px-4 py-3">
        <div class="flex items-center gap-2.5">
          <span class="rounded bg-amber-400 px-1.5 py-0.5 font-mono text-[9px] font-bold text-amber-950">DEMO</span>
          <p class="text-[11px] text-amber-100">展示模式：虛擬節點與模擬流量；所有寫入操作已停用。</p>
        </div>
        <span class="font-mono text-[9px] uppercase tracking-[0.14em] text-amber-300/60">read-only</span>
      </div>

      <main class="panel mt-4 overflow-hidden">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-white/[0.06] px-5 py-4">
          <div class="flex items-center gap-2.5">
            <h3 class="text-sm font-medium text-white">節點清單</h3>
            <span class="rounded-md border border-white/[0.07] bg-white/[0.03] px-1.5 py-0.5 font-mono text-[10px] text-slate-400">
              {{ total }} / {{ page }}
            </span>
          </div>
          <div class="flex items-center gap-3">
            <p class="hidden items-center gap-2 font-mono text-[10px] tracking-[0.1em] text-slate-500 sm:flex">
              <span class="h-1 w-1 rounded-full bg-emerald-400" />
              AUTO REFRESH / {{ POLL_INTERVAL_MS / 1000 }}S
            </p>
            <button
              v-if="canManage && !isSimple"
              type="button"
              class="focus-ring inline-flex items-center gap-2 rounded-lg border border-cyan-400/20 bg-cyan-400/[0.06] px-3 py-2 text-[11px] font-medium text-cyan-200 transition hover:bg-cyan-400/[0.12]"
              @click="batchOpen = true"
            >
              <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                <path d="M4 6h16M4 12h16M4 18h10" stroke-linecap="round" />
              </svg>
              批量操作
            </button>
            <button
              v-if="superAdmin"
              type="button"
              class="focus-ring inline-flex items-center gap-2 rounded-lg bg-emerald-400 px-3 py-2 text-[11px] font-semibold text-emerald-950 transition hover:bg-emerald-300"
              @click="openDeployDialog"
            >
              <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                <path d="M12 5v14M5 12h14" stroke-linecap="round" />
              </svg>
              添加節點
            </button>
          </div>
        </div>

        <div class="flex flex-wrap items-center gap-2 border-b border-white/[0.06] px-5 py-3">
          <input
            v-model="search"
            type="search"
            placeholder="搜尋 node_id / 名稱 / 位址"
            class="focus-ring min-w-[220px] flex-1 rounded-lg border border-white/[0.08] bg-black/25 px-3 py-2 text-xs text-slate-200 outline-none placeholder:text-slate-600 focus:border-emerald-400/40"
            @input="scheduleSearch"
          />
          <select
            v-model="statusFilter"
            class="focus-ring rounded-lg border border-white/[0.08] bg-[#0c1017] px-3 py-2 text-xs text-slate-300 outline-none"
            @change="applyFilters"
          >
            <option value="">全部狀態</option>
            <option value="online">Online</option>
            <option value="offline">Offline</option>
            <option value="provisioning">Provisioning</option>
            <option value="offline_network_error">Network Error</option>
            <option value="gfw_blocked">GFW Blocked</option>
          </select>
          <select
            v-model="tagFilter"
            class="focus-ring rounded-lg border border-white/[0.08] bg-[#0c1017] px-3 py-2 text-xs text-slate-300 outline-none"
            @change="applyFilters"
          >
            <option value="">全部標籤</option>
            <option v-for="tag in tags" :key="tag" :value="tag">{{ tag }}</option>
          </select>
          <button
            type="button"
            class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 font-mono text-[10px] text-slate-400 transition hover:bg-white/[0.05]"
            @click="search = ''; statusFilter = ''; tagFilter = ''; applyFilters()"
          >
            RESET
          </button>
        </div>

        <div class="hidden overflow-x-auto md:block">
          <table class="w-full min-w-[1200px] border-collapse text-left">
            <thead>
              <tr class="border-b border-white/[0.05] text-[10px] uppercase tracking-[0.14em] text-slate-500">
                <th class="px-5 py-3 font-medium">節點</th>
                <th class="px-4 py-3 font-medium">位址</th>
                <th class="px-4 py-3 font-medium">狀態</th>
                <th class="px-4 py-3 font-medium">CPU / RAM</th>
                <th class="px-4 py-3 font-medium">網路</th>
                <th class="px-4 py-3 font-medium">在線 / 最後活動</th>
                <th class="px-4 py-3 font-medium">系統 / Agent</th>
                <th class="px-5 py-3 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-white/[0.045]">
              <template v-if="initialLoading">
                <tr v-for="row in 3" :key="row" class="animate-pulse">
                  <td class="px-5 py-4">
                    <div class="flex items-center gap-3">
                      <div class="h-2 w-2 rounded-full bg-slate-700" />
                      <div class="space-y-2">
                        <div class="h-3 w-28 rounded bg-slate-800" />
                        <div class="h-2 w-20 rounded bg-slate-800/70" />
                      </div>
                    </div>
                  </td>
                  <td class="px-4 py-4"><div class="h-3 w-24 rounded bg-slate-800/80" /></td>
                  <td class="px-4 py-4"><div class="h-5 w-16 rounded bg-slate-800/80" /></td>
                  <td class="px-4 py-4"><div class="h-7 w-28 rounded bg-slate-800/80" /></td>
                  <td class="px-4 py-4"><div class="h-7 w-24 rounded bg-slate-800/80" /></td>
                  <td class="px-4 py-4"><div class="h-3 w-16 rounded bg-slate-800/80" /></td>
                  <td class="px-4 py-4"><div class="h-3 w-28 rounded bg-slate-800/80" /></td>
                  <td class="px-5 py-4"><div class="ml-auto h-8 w-24 rounded bg-slate-800/80" /></td>
                </tr>
              </template>

              <tr v-else-if="nodes.length === 0">
                <td colspan="8" class="px-5 py-16 text-center">
                  <div class="mx-auto grid h-12 w-12 place-items-center rounded-full border border-white/[0.06] bg-white/[0.02]">
                    <svg viewBox="0 0 24 24" class="h-5 w-5 text-slate-600" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                      <path d="M12 5v14M5 12h14" stroke-linecap="round" />
                    </svg>
                  </div>
                  <p class="mt-4 text-sm font-medium text-slate-300">尚無 Agent 連線</p>
                  <p class="mx-auto mt-1.5 max-w-md text-xs leading-5 text-slate-500">
                    使用右上角「添加節點」自動部署，或在目標 Linux 節點以相同 token 手動啟動 Agent。
                  </p>
                  <code class="mt-4 inline-block rounded-lg border border-white/[0.07] bg-black/30 px-3 py-2 font-mono text-[11px] text-emerald-300/90">
                    ./nodehub-agent --server ws://&lt;host&gt;:8080/ws/agent --token &lt;token&gt;
                  </code>
                </td>
              </tr>

              <template v-else>
              <tr
                v-for="node in nodes"
                :key="node.id"
                class="group transition-colors hover:bg-white/[0.02]"
              >
                <td class="px-5 py-4">
                  <div class="flex items-center gap-3">
                    <span class="relative flex h-2 w-2 shrink-0">
                      <span
                        v-if="node.status === 'online'"
                        class="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-20"
                      />
                      <span class="relative inline-flex h-2 w-2 rounded-full" :class="statusDotClasses(node.status)" />
                    </span>
                    <div class="min-w-0">
                      <button
                        type="button"
                        class="focus-ring max-w-[220px] truncate rounded text-left text-sm font-medium text-slate-100 transition hover:text-emerald-200"
                        :aria-label="`開啟 ${node.name || node.id} 詳情`"
                        @click="openNodeDetails(node)"
                      >
                        {{ node.name || node.id }}
                      </button>
                      <p class="mt-0.5 truncate font-mono text-[10px] text-slate-500">{{ node.id }}</p>
                      <div v-if="node.tags?.length" class="mt-1 flex flex-wrap gap-1">
                        <span
                          v-for="tag in node.tags"
                          :key="tag"
                          class="rounded border border-cyan-400/10 bg-cyan-400/[0.04] px-1.5 py-0.5 font-mono text-[9px] text-cyan-300/80"
                        >
                          {{ tag }}
                        </span>
                      </div>
                    </div>
                  </div>
                </td>
                <td class="px-4 py-4">
                  <span class="font-mono text-xs text-slate-300">{{ node.address || node.remote_ip || '—' }}</span>
                </td>
                <td class="px-4 py-4">
                  <span
                    class="inline-flex items-center gap-1.5 rounded-md border px-2 py-1 font-mono text-[10px] uppercase tracking-[0.12em]"
                    :class="statusClasses(node.status)"
                  >
                    <span class="h-1 w-1 rounded-full" :class="statusDotClasses(node.status)" />
                    {{ statusLabel(node.status) }}
                  </span>
                </td>
                <td class="px-4 py-4">
                  <svg
                    v-if="node.cpu_history?.length > 1"
                    viewBox="0 0 96 24"
                    class="mb-1.5 h-6 w-24 overflow-visible"
                    preserveAspectRatio="none"
                    aria-label="CPU sparkline"
                  >
                    <polyline
                      :points="sparklinePoints(node.cpu_history)"
                      fill="none"
                      stroke="rgba(52, 211, 153, .85)"
                      stroke-width="1.4"
                      vector-effect="non-scaling-stroke"
                    />
                  </svg>
                  <div v-if="node.status === 'online' && telemetry[node.id]" class="w-32 space-y-1.5">
                    <div class="flex items-center gap-2">
                      <span class="w-7 font-mono text-[9px] text-slate-500">CPU</span>
                      <div class="h-1 flex-1 overflow-hidden rounded-full bg-white/[0.06]">
                        <div
                          class="h-full rounded-full bg-gradient-to-r from-emerald-500 to-teal-300 transition-all duration-500"
                          :style="{ width: percentWidth(telemetry[node.id]?.cpu_percent) }"
                        />
                      </div>
                      <span class="w-8 text-right font-mono text-[9px] tabular-nums text-emerald-300">
                        {{ formatPercent(telemetry[node.id]?.cpu_percent) }}
                      </span>
                    </div>
                    <div class="flex items-center gap-2">
                      <span class="w-7 font-mono text-[9px] text-slate-500">RAM</span>
                      <div class="h-1 flex-1 overflow-hidden rounded-full bg-white/[0.06]">
                        <div
                          class="h-full rounded-full bg-gradient-to-r from-sky-500 to-cyan-300 transition-all duration-500"
                          :style="{ width: percentWidth(telemetry[node.id]?.memory_percent) }"
                        />
                      </div>
                      <span class="w-8 text-right font-mono text-[9px] tabular-nums text-sky-300">
                        {{ formatPercent(telemetry[node.id]?.memory_percent) }}
                      </span>
                    </div>
                  </div>
                  <span v-else-if="node.status === 'online'" class="font-mono text-[10px] text-slate-600">
                    等待遙測
                  </span>
                  <span v-else class="font-mono text-[10px] text-slate-600">—</span>
                </td>
                <td class="px-4 py-4">
                  <div v-if="node.status === 'online' && telemetry[node.id]" class="space-y-0.5 font-mono text-[10px]">
                    <p class="text-sky-300">
                      ↓ {{ formatRate(telemetry[node.id].rxBytesPerSecond) }}
                    </p>
                    <p class="text-emerald-300">
                      ↑ {{ formatRate(telemetry[node.id].txBytesPerSecond) }}
                    </p>
                  </div>
                  <span v-else class="font-mono text-[10px] text-slate-600">—</span>
                </td>
                <td class="px-4 py-4">
                  <span v-if="node.status === 'online'" class="font-mono text-xs tabular-nums text-slate-300">
                    {{ formatDuration(onlineSeconds(node)) }}
                  </span>
                  <span v-else class="font-mono text-[11px] text-slate-500">
                    {{ lastSeenLabel(node) }}
                  </span>
                </td>
                <td class="px-4 py-4">
                  <p class="text-xs text-slate-300">{{ node.os || '—' }} / {{ node.arch || '—' }}</p>
                  <p class="mt-0.5 truncate font-mono text-[10px] text-slate-500">
                    v{{ node.agent_version || '—' }} · {{ node.plugins.join(', ') || 'no plugins' }}
                  </p>
                </td>
                <td class="px-5 py-4">
                  <div class="flex justify-end gap-2">
                    <button
                      v-if="canManage"
                      type="button"
                      class="focus-ring inline-flex items-center justify-center rounded-lg border border-white/[0.08] bg-white/[0.03] px-2.5 py-2 text-[11px] font-medium text-slate-400 transition hover:border-cyan-400/30 hover:bg-cyan-400/[0.07] hover:text-cyan-200"
                      :aria-label="`${node.name || node.id} 標籤管理`"
                      :title="`${node.name || node.id} 標籤管理`"
                      @click="openTagDialog(node)"
                    >
                      <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                        <path d="M20 13l-7 7-9-9V4h7l9 9z" stroke-linecap="round" stroke-linejoin="round" />
                        <circle cx="8.5" cy="7.5" r="1" fill="currentColor" stroke="none" />
                      </svg>
                    </button>
                    <button
                      type="button"
                      class="focus-ring inline-flex items-center gap-1.5 rounded-lg border border-white/[0.08] bg-white/[0.03] px-2.5 py-2 text-[11px] font-medium text-slate-300 transition hover:border-sky-400/30 hover:bg-sky-400/[0.07] hover:text-sky-200"
                      :aria-label="`${node.name || node.id} 詳情與端口轉發`"
                      @click="openNodeDetails(node)"
                    >
                      詳情
                    </button>
                    <button
                      v-if="canManage && node.status === 'online'"
                      type="button"
                      class="focus-ring inline-flex items-center justify-center rounded-lg border border-white/[0.08] bg-white/[0.03] px-2.5 py-2 text-[11px] font-medium text-slate-300 transition hover:border-emerald-400/30 hover:bg-emerald-400/[0.07] hover:text-emerald-200 disabled:cursor-not-allowed disabled:opacity-50"
                      :disabled="commandBusy"
                      :aria-label="`獲取 ${node.name || node.id} 的系統資訊`"
                      :title="`獲取 ${node.name || node.id} 的系統資訊`"
                      @click="inspect(node)"
                    >
                      <svg
                        v-if="commandLoading && selectedNode?.id === node.id"
                        viewBox="0 0 24 24"
                        class="h-3.5 w-3.5 animate-spin"
                        fill="none"
                        stroke="currentColor"
                        stroke-width="2"
                        aria-hidden="true"
                      >
                        <path d="M21 12a9 9 0 1 1-6.2-8.56" stroke-linecap="round" />
                      </svg>
                      <svg
                        v-else
                        viewBox="0 0 24 24"
                        class="h-3.5 w-3.5"
                        fill="none"
                        stroke="currentColor"
                        stroke-width="1.8"
                        aria-hidden="true"
                      >
                        <path d="m8 9 3-3 3 3M8 15l3 3 3-3M12 6v12" stroke-linecap="round" stroke-linejoin="round" />
                      </svg>
                    </button>
                  </div>
                </td>
              </tr>
              </template>
            </tbody>
          </table>
        </div>

        <div class="divide-y divide-white/[0.045] md:hidden">
          <button
            v-for="node in nodes"
            :key="`mobile-${node.id}`"
            type="button"
            class="flex w-full items-center gap-3 px-4 py-4 text-left transition active:bg-white/[0.04]"
            @click="openNodeDetails(node)"
          >
            <span class="h-2 w-2 shrink-0 rounded-full" :class="node.status === 'online' ? 'bg-emerald-400' : node.status === 'offline' ? 'bg-slate-600' : 'bg-amber-400'"></span>
            <span class="min-w-0 flex-1">
              <span class="block truncate text-sm font-medium text-slate-100">{{ node.name }}</span>
              <span class="mt-0.5 block truncate font-mono text-[10px] text-slate-500">{{ node.address || 'no address' }} · {{ node.status }}</span>
              <span v-if="telemetry[node.id]" class="mt-1 block font-mono text-[9px] text-slate-600">CPU {{ formatPercent(telemetry[node.id]?.cpu_percent) }} · RAM {{ formatPercent(telemetry[node.id]?.memory_percent) }}</span>
            </span>
            <svg viewBox="0 0 24 24" class="h-4 w-4 shrink-0 text-slate-600" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="m9 6 6 6-6 6" stroke-linecap="round" stroke-linejoin="round" /></svg>
          </button>
        </div>

        <div class="flex flex-wrap items-center justify-between gap-3 border-t border-white/[0.06] px-5 py-3">
          <p class="font-mono text-[10px] text-slate-600">
            {{ total }} NODE{{ total === 1 ? '' : 'S' }} · PAGE {{ page }} / {{ Math.max(1, totalPages) }}
          </p>
          <div class="flex items-center gap-2">
            <button
              type="button"
              class="focus-ring rounded-md border border-white/[0.08] px-2.5 py-1 font-mono text-[10px] text-slate-400 transition disabled:opacity-30"
              :disabled="page <= 1"
              @click="goPage(page - 1)"
            >
              PREV
            </button>
            <span class="font-mono text-[10px] text-slate-500">{{ page }} / {{ Math.max(1, totalPages) }}</span>
            <button
              type="button"
              class="focus-ring rounded-md border border-white/[0.08] px-2.5 py-1 font-mono text-[10px] text-slate-400 transition disabled:opacity-30"
              :disabled="page >= totalPages"
              @click="goPage(page + 1)"
            >
              NEXT
            </button>
          </div>
        </div>
      </main>

      <footer class="mt-auto flex flex-wrap items-center justify-between gap-2 pb-2 pt-8">
        <p class="font-mono text-[10px] tracking-[0.08em] text-slate-600">
          NODEHUB MVP · CONTROL PLANE / EDGE AGENT / WEB
        </p>
        <p class="font-mono text-[10px] text-slate-600">
          plugin: system_info
        </p>
      </footer>
    </div>

    <SystemInfoDialog
      :open="dialogOpen"
      :title="dialogTitle"
      :loading="commandLoading"
      :result="commandResult"
      :error="commandError"
      @close="closeDialog"
    />

    <DeployNodeDialog
      :open="deployOpen"
      :loading="deployLoading"
      :result="deployResult"
      :error="deployError"
      @close="closeDeployDialog"
      @submit="submitDeploy"
    />

    <NodeDetailDialog
      :open="detailOpen"
      :node="detailNode"
      @close="closeNodeDetails"
      @refresh="refresh(true)"
    />

    <ChangePasswordDialog
      :open="passwordOpen"
      :username="currentUser?.username ?? 'admin'"
      @close="passwordOpen = false"
    />

    <TagDialog
      :open="tagOpen"
      :node="tagNode"
      @close="closeTagDialog"
      @saved="refresh(true)"
    />

    <BatchDialog :open="batchOpen" @close="batchOpen = false" />
  </div>
</template>
