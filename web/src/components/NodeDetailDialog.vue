<script setup lang="ts">
import QRCode from 'qrcode'
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import {
  ApiError,
  cloudPower,
  createPortForward,
  deploySite,
  listCertificates,
  listCloudCredentials,
  listSiteTemplates,
  saveCloudCredential,
  testCloudCredential,
  createProxyInbound,
  deletePortForward,
  deleteProxyInbound,
  ddNode,
  deleteNode,
  deleteXrayConfig,
  generateRealityKeys,
  getCloudBandwidth,
  getInboundShare,
  getNode,
  getXrayConfig,
  getXrayStatus,
  restartXray,
  updateNodeIdentity,
  listPortForwards,
  listProxyInbounds,
  listUserTraffic,
  replaceNodeIP,
  runSpeedtest,
  saveXrayConfig,
  updateNodeCloud,
  updatePortForward,
  updateProxyInbound,
} from '../api'
import { canWrite, getUser, isSuperAdmin } from '../auth'
import { useUIMode } from '../uiMode'
import type {
  AgentNode,
  CertificateView,
  CloudBandwidth,
  SiteTemplate,
  DDResponse,
  InboundNetwork,
  InboundProtocol,
  InboundSecurity,
  NodeStatus,
  PortForwardRule,
  ProxyInbound,
  ProxyInboundRequest,
  ProxyInboundSettings,
  RealityKeysResponse,
  ReplaceIPResponse,
  SpeedtestResult,
  UserTraffic,
} from '../types'

const props = defineProps<{
  open: boolean
  node: AgentNode | null
}>()

const emit = defineEmits<{
  close: []
  refresh: []
}>()

const details = ref<AgentNode | null>(null)
const rules = ref<PortForwardRule[]>([])
const initialLoading = ref(false)
const error = ref('')
const formError = ref('')
const warning = ref('')
const saving = ref(false)
const busyRule = ref<string | null>(null)
const hostInput = ref<HTMLInputElement | null>(null)

const xrayConfigText = ref('')
const xrayExists = ref(false)
const xrayUpdatedAt = ref<string | null>(null)
const xrayLoading = ref(false)
const xraySaving = ref(false)
const xrayDeleting = ref(false)
const xrayError = ref('')
const xrayWarning = ref('')
const xrayStatus = ref<Record<string, unknown> | null>(null)
const xrayStatusLoading = ref(false)
const xrayRestarting = ref(false)

const inbounds = ref<ProxyInbound[]>([])
const inboundNodeHost = ref('')
const inboundsLoading = ref(false)
const inboundSaving = ref(false)
const inboundDeletingID = ref<number | null>(null)
const inboundsError = ref('')
const inboundsWarning = ref('')
const inboundDialogOpen = ref(false)
const editingInbound = ref<ProxyInbound | null>(null)
const inboundForm = reactive({
  remark: '',
  port: 443,
  protocol: 'vless' as InboundProtocol,
  network: 'tcp' as InboundNetwork,
  security: 'none' as InboundSecurity,
  uuid: '',
  flow: '',
  password: '',
  method: '2022-blake3-aes-128-gcm',
  path: '/',
  host: '',
  serviceName: 'nodehub',
  privateKey: '',
  publicKey: '',
  shortID: '',
  dest: 'www.microsoft.com:443',
  serverName: '',
  certificateFile: '',
  keyFile: '',
})
const realityGenerating = ref(false)
const inboundFormError = ref('')
const shareOpen = ref(false)
const shareLoading = ref(false)
const shareError = ref('')
const shareInbound = ref<ProxyInbound | null>(null)
const shareLink = ref('')
const shareQR = ref('')
const shareCopied = ref(false)

type DetailTab = 'basic' | 'subscriptions' | 'port-forward' | 'inbounds' | 'xray' | 'traffic' | 'cloud'
const { isSimple } = useUIMode()
const activeTab = ref<DetailTab>('basic')
watch(isSimple, (simple) => {
  if (simple && activeTab.value !== 'basic' && activeTab.value !== 'subscriptions') {
    activeTab.value = 'basic'
  }
})
const traffic = ref<UserTraffic[]>([])
const trafficLoading = ref(false)
const trafficError = ref('')
let trafficTimer: number | undefined

const speedtestLoading = ref(false)
const speedtestError = ref('')
const speedtestResult = ref<SpeedtestResult | null>(null)

const cloudAutoHeal = ref(false)
const cloudProvider = ref('')
const cloudInstanceID = ref('')
const ddnsDomain = ref('')
const ddnsZoneID = ref('')
const ddnsRecordID = ref('')
const cloudSaving = ref(false)
const cloudError = ref('')
const cloudWarning = ref('')
const replaceConfirmOpen = ref(false)
const currentUser = getUser()
const canManage = computed(() => canWrite(currentUser))
const superAdmin = computed(() => isSuperAdmin(currentUser))
const cloudActionLoading = ref('')
const cloudBandwidth = ref<CloudBandwidth | null>(null)
const cloudBandwidthLoading = ref(false)
const cloudControlError = ref('')
const ddConfirmOpen = ref(false)
const ddLoading = ref(false)
const ddError = ref('')
const ddResult = ref<DDResponse | null>(null)
const ddForm = reactive({
  distro: 'debian' as 'debian' | 'ubuntu',
  version: '12',
  arch: 'amd64' as 'amd64' | 'arm64',
  rootPassword: '',
})
const deleteConfirmOpen = ref(false)
const deleteConfirmText = ref('')
const deleteLoading = ref(false)
const deleteError = ref('')
const identityOpen = ref(false)
const identityName = ref('')
const identityAddress = ref('')
const identitySaving = ref(false)
const identityError = ref('')
const certificates = ref<CertificateView[]>([])
const siteTemplates = ref<SiteTemplate[]>([])
const selectedCertificateID = ref<number | null>(null)
const selectedTemplateID = ref('corporate')
const siteDeploying = ref(false)
const siteError = ref('')
const siteResult = ref('')
const cloudCredentials = ref<{ provider: string; masked_key: string }[]>([])
const credentialProvider = ref('vultr')
const credentialKey = ref('')
const credentialSaving = ref(false)
const credentialTesting = ref(false)
const credentialMessage = ref('')
const credentialError = ref('')
const replacing = ref(false)
const replaceResult = ref<ReplaceIPResponse | null>(null)
const form = reactive<{
  listen_port: number | ''
  protocol: 'tcp' | 'udp'
  target_host: string
  target_port: number | ''
  enabled: boolean
}>({
  listen_port: 8080,
  protocol: 'tcp',
  target_host: '',
  target_port: 80,
  enabled: true,
})

let previousOverflow = ''
let requestSeq = 0

const activeNode = computed(() => details.value ?? props.node)
const supportsPortForward = computed(
  () => activeNode.value?.plugins.includes('port_forward') ?? false,
)
const supportsXray = computed(
  () => activeNode.value?.plugins.includes('xray') ?? false,
)
const supportsSpeedtest = computed(
  () => activeNode.value?.plugins.includes('speedtest') ?? false,
)
const isNodeOnline = computed(
  () => activeNode.value?.status === 'online' || activeNode.value?.status === 'gfw_blocked',
)
const xrayBusy = computed(() => xraySaving.value || xrayDeleting.value)
const inboundBusy = computed(() => inboundSaving.value || inboundDeletingID.value !== null)
const availableNetworks = computed<InboundNetwork[]>(() => {
  if (inboundForm.protocol === 'shadowsocks') return ['tcp']
  return ['tcp', 'ws', 'grpc']
})
const availableSecurities = computed<InboundSecurity[]>(() => {
  if (inboundForm.protocol === 'shadowsocks') return ['none']
  if (inboundForm.protocol === 'trojan') return ['none', 'tls']
  if (inboundForm.network !== 'tcp') return ['none', 'tls']
  return ['none', 'tls', 'reality']
})
const showFlow = computed(
  () =>
    inboundForm.protocol === 'vless' &&
    inboundForm.network === 'tcp' &&
    (inboundForm.security === 'tls' || inboundForm.security === 'reality'),
)
const isReality = computed(() => inboundForm.security === 'reality')
const isTLS = computed(() => inboundForm.security === 'tls')
const isWebSocket = computed(() => inboundForm.network === 'ws')
const isGRPC = computed(() => inboundForm.network === 'grpc')
const totalTrafficBytes = computed(() =>
  traffic.value.reduce((sum, user) => sum + user.total_bytes, 0),
)
const footerSummary = computed(() => {
  if (activeTab.value === 'traffic') {
    return `${traffic.value.length} USERS / ${formatBytes(totalTrafficBytes.value)}`
  }
  if (activeTab.value === 'inbounds') {
    return `${inbounds.value.length} INBOUNDS / ${inboundNodeHost.value || 'NO HOST'}`
  }
  if (activeTab.value === 'xray') {
    return xrayExists.value ? 'XRAY CONFIGURED' : 'XRAY NOT CONFIGURED'
  }
  if (activeTab.value === 'cloud') {
    return cloudAutoHeal.value ? 'AUTO-HEAL ENABLED' : 'AUTO-HEAL DISABLED'
  }
  if (activeTab.value === 'basic') {
    return `${activeNode.value?.status?.toUpperCase() ?? 'UNKNOWN'} / ${activeNode.value?.agent_version ? 'AGENT ' + activeNode.value.agent_version : 'NO AGENT'}`
  }
  if (activeTab.value === 'subscriptions') {
    return `${inbounds.value.length} SUBSCRIPTION${inbounds.value.length === 1 ? '' : 'S'}`
  }
  return `${rules.value.filter((rule) => rule.enabled).length} ENABLED / ${rules.value.length} TOTAL`
})

watch(
  () => inboundForm.protocol,
  () => {
    if (!availableNetworks.value.includes(inboundForm.network)) {
      inboundForm.network = availableNetworks.value[0]
    }
    if (!availableSecurities.value.includes(inboundForm.security)) {
      inboundForm.security = availableSecurities.value[0]
    }
    if (isSimple.value) {
      inboundForm.network = 'tcp'
      inboundForm.security = 'none'
      if (inboundDialogOpen.value) generateInboundSecret()
    }
  },
)
watch(
  () => inboundForm.network,
  () => {
    if (!availableSecurities.value.includes(inboundForm.security)) {
      inboundForm.security = availableSecurities.value[0]
    }
  },
)
watch(
  () => inboundForm.security,
  (security) => {
    if (security === 'reality') {
      inboundForm.network = 'tcp'
      if (!inboundForm.flow) inboundForm.flow = 'xtls-rprx-vision'
    }
    if (security === 'none' && !showFlow.value) {
      inboundForm.flow = ''
    }
  },
)

function messageOf(value: unknown): string {
  if (value instanceof ApiError) return value.message
  if (value instanceof Error) return value.message
  return String(value)
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

function formatLastSeen(value: string | undefined): string {
  if (!value) return '從未上線'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString()
}

function formatTarget(rule: PortForwardRule): string {
  const host = rule.target_host.includes(':') ? `[${rule.target_host}]` : rule.target_host
  return `${host}:${rule.target_port}`
}

function formatPercent(value: number | undefined): string {
  return typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(0)}%` : '—'
}

function formatUptime(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds))
  const days = Math.floor(total / 86_400)
  const hours = Math.floor((total % 86_400) / 3_600)
  const minutes = Math.floor((total % 3_600) / 60)
  if (days > 0) return `${days}d ${hours}h`
  return `${hours}h ${minutes}m`
}

function formatBytes(value: number | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024
    unit += 1
  }
  return `${amount.toFixed(amount >= 100 ? 0 : 2)} ${units[unit]}`
}

function setSyncWarning(synced: boolean, message?: string): void {
  warning.value = synced
    ? ''
    : message || '規則已儲存；Agent 離線時會在下一次回連後套用。'
}

async function loadAll(): Promise<void> {
  const node = props.node
  if (!node) return
  const seq = ++requestSeq
  initialLoading.value = true
  error.value = ''
  xrayLoading.value = true
  inboundsLoading.value = true
  try {
    const [nodeResponse, rulesResponse, xrayResponse, inboundsResponse, trafficResponse] = await Promise.all([
      getNode(node.id),
      listPortForwards(node.id),
      getXrayConfig(node.id),
      listProxyInbounds(node.id),
      listUserTraffic(node.id),
    ])
    if (seq !== requestSeq) return
    details.value = nodeResponse.node
    syncCloudForm(nodeResponse.node)
    rules.value = rulesResponse.rules
    inbounds.value = inboundsResponse.inbounds
    inboundNodeHost.value = inboundsResponse.node_host
    traffic.value = trafficResponse.users
    if (xrayResponse) {
      xrayExists.value = true
      xrayConfigText.value = JSON.stringify(xrayResponse.config, null, 2)
      xrayUpdatedAt.value = xrayResponse.updated_at
    } else {
      xrayExists.value = false
      xrayConfigText.value = ''
      xrayUpdatedAt.value = null
    }
  } catch (value) {
    if (seq === requestSeq) error.value = messageOf(value)
  } finally {
    if (seq === requestSeq) {
      initialLoading.value = false
      xrayLoading.value = false
      inboundsLoading.value = false
    }
  }
}

function syncCloudForm(node: AgentNode): void {
  cloudAutoHeal.value = node.auto_heal ?? false
  cloudProvider.value = node.cloud_provider || ''
  cloudInstanceID.value = node.provider_instance_id || ''
  ddnsDomain.value = node.ddns_domain || ''
  ddnsZoneID.value = node.ddns_zone_id || ''
  ddnsRecordID.value = node.ddns_record_id || ''
}

async function loadXrayStatus(): Promise<void> {
  if (!props.node || xrayStatusLoading.value) return
  xrayStatusLoading.value = true
  xrayError.value = ''
  try {
    const response = await getXrayStatus(props.node.id)
    xrayStatus.value = response.status as Record<string, unknown>
  } catch (error) {
    xrayError.value = messageOf(error)
  } finally {
    xrayStatusLoading.value = false
  }
}

async function restartXrayProcess(): Promise<void> {
  if (!props.node || xrayRestarting.value) return
  xrayRestarting.value = true
  xrayError.value = ''
  try {
    await restartXray(props.node.id)
    xrayWarning.value = 'Xray 重啟已在背景排隊'
  } catch (error) {
    xrayError.value = messageOf(error)
  } finally {
    xrayRestarting.value = false
  }
}

async function loadCloudResources(): Promise<void> {
  if (!superAdmin.value) return
  try {
    const [certs, templates, credentials] = await Promise.all([
      listCertificates(),
      listSiteTemplates(),
      listCloudCredentials(),
    ])
    certificates.value = certs.certificates
    siteTemplates.value = templates.templates
    cloudCredentials.value = credentials.credentials
    if (selectedCertificateID.value === null && certs.certificates.length > 0) {
      selectedCertificateID.value = certs.certificates[0].id
    }
    if (templates.templates.length > 0 && !templates.templates.some((item) => item.id === selectedTemplateID.value)) {
      selectedTemplateID.value = templates.templates[0].id
    }
  } catch {
    // Advanced resources are optional for non-pro deployments.
  }
}

async function saveCredential(): Promise<void> {
  if (credentialSaving.value || !credentialKey.value.trim()) return
  credentialSaving.value = true
  credentialError.value = ''
  credentialMessage.value = ''
  try {
    await saveCloudCredential(credentialProvider.value, credentialKey.value.trim())
    credentialKey.value = ''
    credentialMessage.value = 'API Key 已儲存'
    await loadCloudResources()
  } catch (error) {
    credentialError.value = messageOf(error)
  } finally {
    credentialSaving.value = false
  }
}

async function testCredential(): Promise<void> {
  if (credentialTesting.value) return
  credentialTesting.value = true
  credentialError.value = ''
  credentialMessage.value = ''
  try {
    await testCloudCredential(credentialProvider.value, credentialKey.value.trim())
    credentialMessage.value = '連通性測試成功'
  } catch (error) {
    credentialError.value = messageOf(error)
  } finally {
    credentialTesting.value = false
  }
}

async function deployFakeSite(): Promise<void> {
  if (!props.node || siteDeploying.value || selectedCertificateID.value === null) return
  siteDeploying.value = true
  siteError.value = ''
  siteResult.value = ''
  try {
    const response = await deploySite(props.node.id, selectedCertificateID.value, selectedTemplateID.value)
    siteResult.value = `${response.domain} · ${response.template} · ${response.web_root}`
    emit('refresh')
  } catch (error) {
    siteError.value = messageOf(error)
  } finally {
    siteDeploying.value = false
  }
}

async function powerCloud(action: 'off' | 'on' | 'reboot'): Promise<void> {
  if (!props.node || cloudActionLoading.value) return
  cloudControlError.value = ''
  cloudActionLoading.value = action
  try {
    await cloudPower(props.node.id, action)
    cloudWarning.value = `雲端主機指令已送出：${action}`
    emit('refresh')
  } catch (error) {
    cloudControlError.value = messageOf(error)
  } finally {
    cloudActionLoading.value = ''
  }
}

async function loadCloudBandwidth(): Promise<void> {
  if (!props.node || cloudBandwidthLoading.value) return
  cloudBandwidthLoading.value = true
  cloudControlError.value = ''
  try {
    const response = await getCloudBandwidth(props.node.id)
    cloudBandwidth.value = response.bandwidth
  } catch (error) {
    cloudControlError.value = messageOf(error)
  } finally {
    cloudBandwidthLoading.value = false
  }
}

function openDDConfirm(): void {
  ddError.value = ''
  ddResult.value = null
  ddConfirmOpen.value = true
}

function openIdentityEditor(): void {
  identityName.value = activeNode.value?.name || ''
  identityAddress.value = activeNode.value?.address || ''
  identityError.value = ''
  identityOpen.value = true
}

async function saveIdentity(): Promise<void> {
  const node = activeNode.value
  if (!node || identitySaving.value) return
  identitySaving.value = true
  identityError.value = ''
  try {
    const response = await updateNodeIdentity(node.id, identityName.value.trim(), identityAddress.value.trim())
    details.value = response.node
    identityOpen.value = false
    emit('refresh')
  } catch (error) {
    identityError.value = messageOf(error)
  } finally {
    identitySaving.value = false
  }
}

function openDeleteConfirm(): void {
  deleteConfirmText.value = ''
  deleteError.value = ''
  deleteConfirmOpen.value = true
}

async function confirmDeleteNode(): Promise<void> {
  const node = activeNode.value
  if (!node || deleteLoading.value) return
  if (deleteConfirmText.value.trim() !== node.id) {
    deleteError.value = '請輸入完整 Node ID 以確認'
    return
  }
  deleteLoading.value = true
  deleteError.value = ''
  try {
    await deleteNode(node.id)
    deleteConfirmOpen.value = false
    emit('refresh')
    emit('close')
  } catch (error) {
    deleteError.value = messageOf(error)
  } finally {
    deleteLoading.value = false
  }
}

async function submitDD(): Promise<void> {
  if (!props.node || ddLoading.value) return
  ddLoading.value = true
  ddError.value = ''
  ddResult.value = null
  try {
    ddResult.value = await ddNode(props.node.id, {
      distro: ddForm.distro,
      version: ddForm.version,
      arch: ddForm.arch,
      root_password: ddForm.rootPassword || undefined,
    })
    ddConfirmOpen.value = false
  } catch (error) {
    ddError.value = messageOf(error)
  } finally {
    ddLoading.value = false
  }
}

async function saveCloud(): Promise<void> {
  if (!props.node || cloudSaving.value) return
  cloudError.value = ''
  cloudWarning.value = ''
  const needsInstance = cloudProvider.value !== '' && cloudProvider.value !== 'adsl'
  if (cloudAutoHeal.value && (!cloudProvider.value || (needsInstance && !cloudInstanceID.value.trim()))) {
    cloudError.value = '啟用 Auto-Heal 前必須設定 Cloud Provider；非 ADSL 另需 Instance ID'
    return
  }
  if (needsInstance && !cloudInstanceID.value.trim()) {
    cloudError.value = 'Provider Instance ID 不可為空'
    return
  }
  if (ddnsDomain.value.trim() && !ddnsZoneID.value.trim()) {
    cloudError.value = '設定 DDNS Domain 時必須填寫 Zone ID'
    return
  }
  cloudSaving.value = true
  try {
    const response = await updateNodeCloud(props.node.id, {
      cloud_provider: cloudProvider.value,
      provider_instance_id: cloudInstanceID.value.trim(),
      ddns_domain: ddnsDomain.value.trim(),
      ddns_zone_id: ddnsZoneID.value.trim(),
      ddns_record_id: ddnsRecordID.value.trim(),
      auto_heal: cloudAutoHeal.value,
    })
    details.value = response.node
    syncCloudForm(response.node)
    cloudWarning.value = '雲端自癒設定已儲存'
    emit('refresh')
  } catch (value) {
    cloudError.value = messageOf(value)
  } finally {
    cloudSaving.value = false
  }
}

function openReplaceConfirm(): void {
  if (replacing.value) return
  cloudError.value = ''
  cloudWarning.value = ''
  replaceResult.value = null
  replaceConfirmOpen.value = true
}

async function confirmReplaceIP(): Promise<void> {
  if (!props.node || replacing.value) return
  replacing.value = true
  cloudError.value = ''
  try {
    const response = await replaceNodeIP(props.node.id)
    replaceResult.value = response
    replaceConfirmOpen.value = false
    if (details.value) {
      details.value = { ...details.value, remote_ip: response.new_ip }
    }
    emit('refresh')
  } catch (value) {
    cloudError.value = messageOf(value)
    replaceConfirmOpen.value = false
  } finally {
    replacing.value = false
  }
}

async function refreshRules(): Promise<void> {
  if (!props.node) return
  try {
    const response = await listPortForwards(props.node.id)
    rules.value = response.rules
  } catch (value) {
    error.value = messageOf(value)
  }
}

async function runNodeSpeedtest(): Promise<void> {
  if (!props.node || speedtestLoading.value) return
  speedtestError.value = ''
  speedtestResult.value = null
  speedtestLoading.value = true
  try {
    const response = await runSpeedtest(props.node.id)
    speedtestResult.value = response.result
  } catch (value) {
    speedtestError.value = messageOf(value)
  } finally {
    speedtestLoading.value = false
  }
}

async function loadTraffic(): Promise<void> {
  const nodeID = props.node?.id
  if (!nodeID) return
  trafficLoading.value = true
  trafficError.value = ''
  try {
    const response = await listUserTraffic(nodeID)
    if (props.node?.id === nodeID) traffic.value = response.users
  } catch (value) {
    if (props.node?.id === nodeID) trafficError.value = messageOf(value)
  } finally {
    if (props.node?.id === nodeID) trafficLoading.value = false
  }
}

function stopTrafficTimer(): void {
  if (trafficTimer !== undefined) {
    window.clearInterval(trafficTimer)
    trafficTimer = undefined
  }
}

function selectTab(tab: DetailTab): void {
  activeTab.value = tab
  if (tab === 'cloud') void loadCloudResources()
  if (tab === 'xray') void loadXrayStatus()
  if (tab === 'traffic') {
    void loadTraffic()
    if (trafficTimer === undefined) {
      trafficTimer = window.setInterval(() => void loadTraffic(), 30_000)
    }
  } else {
    stopTrafficTimer()
  }
}

async function addRule(): Promise<void> {
  if (!props.node || saving.value) return
  formError.value = ''
  error.value = ''
  const listenPort = Number(form.listen_port)
  const targetPort = Number(form.target_port)
  const targetHost = form.target_host.trim()
  if (!Number.isInteger(listenPort) || listenPort < 1 || listenPort > 65535) {
    formError.value = '本機端口必須介於 1–65535'
    return
  }
  if (!targetHost) {
    formError.value = '請輸入目標主機或域名'
    return
  }
  if (!Number.isInteger(targetPort) || targetPort < 1 || targetPort > 65535) {
    formError.value = '目標端口必須介於 1–65535'
    return
  }

  saving.value = true
  try {
    const response = await createPortForward(props.node.id, {
      listen_port: listenPort,
      protocol: form.protocol,
      target_host: targetHost,
      target_port: targetPort,
      enabled: form.enabled,
    })
    setSyncWarning(response.synced, response.warning)
    form.target_host = ''
    await refreshRules()
    emit('refresh')
  } catch (value) {
    formError.value = messageOf(value)
  } finally {
    saving.value = false
  }
}

async function toggleRule(rule: PortForwardRule): Promise<void> {
  if (!props.node || busyRule.value) return
  busyRule.value = rule.rule_id
  error.value = ''
  try {
    const response = await updatePortForward(props.node.id, rule.rule_id, {
      enabled: !rule.enabled,
    })
    setSyncWarning(response.synced, response.warning)
    await refreshRules()
    emit('refresh')
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    busyRule.value = null
  }
}

async function removeRule(rule: PortForwardRule): Promise<void> {
  if (!props.node || busyRule.value) return
  if (!window.confirm(`刪除本機端口 ${rule.listen_port} 的轉發規則？`)) return
  busyRule.value = rule.rule_id
  error.value = ''
  try {
    const response = await deletePortForward(props.node.id, rule.rule_id)
    setSyncWarning(response.synced, response.warning)
    await refreshRules()
    emit('refresh')
  } catch (value) {
    error.value = messageOf(value)
  } finally {
    busyRule.value = null
  }
}

function setXraySyncWarning(synced: boolean, message?: string): void {
  xrayWarning.value = synced
    ? ''
    : message || 'Xray 配置已儲存；Agent 離線或尚未套用，將在下一次回連後同步。'
}

function formatXrayJSON(): void {
  xrayError.value = ''
  try {
    const parsed = JSON.parse(xrayConfigText.value) as unknown
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      throw new Error('Xray 配置必須是 JSON 物件')
    }
    xrayConfigText.value = JSON.stringify(parsed, null, 2)
  } catch (value) {
    xrayError.value = value instanceof Error ? value.message : 'Xray JSON 格式錯誤'
  }
}

async function saveXray(): Promise<void> {
  if (!props.node || xraySaving.value || xrayDeleting.value) return
  xrayError.value = ''
  let parsed: unknown
  try {
    parsed = JSON.parse(xrayConfigText.value)
  } catch {
    xrayError.value = 'Xray 配置不是有效的 JSON'
    return
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    xrayError.value = 'Xray 配置必須是 JSON 物件'
    return
  }

  xraySaving.value = true
  try {
    const response = await saveXrayConfig(props.node.id, parsed as Record<string, unknown>)
    xrayExists.value = true
    if (response.config) {
      xrayConfigText.value = JSON.stringify(response.config, null, 2)
    }
    xrayUpdatedAt.value = response.updated_at ?? null
    setXraySyncWarning(response.synced, response.warning)
  } catch (value) {
    xrayError.value = messageOf(value)
  } finally {
    xraySaving.value = false
  }
}

async function removeXray(): Promise<void> {
  if (!props.node || xraySaving.value || xrayDeleting.value) return
  if (!window.confirm('刪除 Xray 配置並停止 Xray 進程？')) return
  xrayDeleting.value = true
  xrayError.value = ''
  try {
    const response = await deleteXrayConfig(props.node.id)
    xrayExists.value = false
    xrayConfigText.value = ''
    xrayUpdatedAt.value = null
    setXraySyncWarning(response.synced, response.warning)
  } catch (value) {
    xrayError.value = messageOf(value)
  } finally {
    xrayDeleting.value = false
  }
}

function resetInboundForm(): void {
  inboundForm.remark = ''
  inboundForm.port = 443
  inboundForm.protocol = 'vless'
  inboundForm.network = 'tcp'
  inboundForm.security = 'none'
  inboundForm.uuid = ''
  inboundForm.flow = ''
  inboundForm.password = ''
  inboundForm.method = '2022-blake3-aes-128-gcm'
  inboundForm.path = '/'
  inboundForm.host = ''
  inboundForm.serviceName = 'nodehub'
  inboundForm.privateKey = ''
  inboundForm.publicKey = ''
  inboundForm.shortID = ''
  inboundForm.dest = 'www.microsoft.com:443'
  inboundForm.serverName = ''
  inboundForm.certificateFile = ''
  inboundForm.keyFile = ''
  inboundFormError.value = ''
}

function fillInboundForm(inbound: ProxyInbound): void {
  const settings = inbound.settings || {}
  inboundForm.remark = inbound.remark
  inboundForm.port = inbound.port
  inboundForm.protocol = inbound.protocol
  inboundForm.network = inbound.network
  inboundForm.security = inbound.security
  inboundForm.uuid = settings.uuid || ''
  inboundForm.flow = settings.flow || ''
  inboundForm.password = settings.password || ''
  inboundForm.method = settings.method || '2022-blake3-aes-128-gcm'
  inboundForm.path = settings.path || '/'
  inboundForm.host = settings.host || ''
  inboundForm.serviceName = settings.service_name || 'nodehub'
  inboundForm.privateKey = settings.private_key || ''
  inboundForm.publicKey = settings.public_key || ''
  inboundForm.shortID = settings.short_id || ''
  inboundForm.dest = settings.dest || 'www.microsoft.com:443'
  inboundForm.serverName = settings.server_name || ''
  inboundForm.certificateFile = settings.certificate_file || ''
  inboundForm.keyFile = settings.key_file || ''
}

function openInboundDialog(inbound?: ProxyInbound): void {
  resetInboundForm()
  editingInbound.value = inbound ?? null
  if (inbound) {
    fillInboundForm(inbound)
  } else {
    inboundForm.uuid = randomUUID()
    inboundForm.port = 443
    inboundForm.remark = ''
    if (isSimple.value) {
      inboundForm.network = 'tcp'
      inboundForm.security = 'none'
      inboundForm.flow = ''
      inboundForm.path = '/'
      inboundForm.serviceName = 'nodehub'
      generateInboundSecret()
    }
  }
  inboundDialogOpen.value = true
}

function closeInboundDialog(): void {
  if (inboundSaving.value) return
  inboundDialogOpen.value = false
  editingInbound.value = null
  inboundFormError.value = ''
}

function randomBytes(length: number): Uint8Array {
  const bytes = new Uint8Array(length)
  crypto.getRandomValues(bytes)
  return bytes
}

function randomUUID(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  const bytes = randomBytes(16)
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

function randomPassword(length = 24): string {
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'
  const bytes = randomBytes(length)
  return Array.from(bytes, (value) => alphabet[value % alphabet.length]).join('')
}

function randomBase64(bytes: number): string {
  return btoa(String.fromCharCode(...randomBytes(bytes)))
}

function generateInboundSecret(): void {
  if (inboundForm.protocol === 'vless') {
    inboundForm.uuid = randomUUID()
    return
  }
  if (inboundForm.protocol === 'shadowsocks') {
    const keyBytes = inboundForm.method.includes('128') ? 16 : 32
    inboundForm.password = inboundForm.method.startsWith('2022') ? randomBase64(keyBytes) : randomPassword()
    return
  }
  inboundForm.password = randomPassword()
}

async function generateRealityPair(): Promise<void> {
  if (!props.node || realityGenerating.value) return
  realityGenerating.value = true
  inboundFormError.value = ''
  try {
    const keys: RealityKeysResponse = await generateRealityKeys(props.node.id)
    inboundForm.privateKey = keys.private_key
    inboundForm.publicKey = keys.public_key
    inboundForm.shortID = keys.short_id
    if (!inboundForm.flow) inboundForm.flow = 'xtls-rprx-vision'
  } catch (value) {
    inboundFormError.value = messageOf(value)
  } finally {
    realityGenerating.value = false
  }
}

function buildInboundPayload(): ProxyInboundRequest | null {
  inboundFormError.value = ''
  const port = Number(inboundForm.port)
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    inboundFormError.value = '端口必須介於 1–65535'
    return null
  }
  if (port === 10085) {
    inboundFormError.value = '10085 保留給 Xray Stats API'
    return null
  }
  if (!inboundForm.remark.trim()) {
    inboundFormError.value = '請輸入備註'
    return null
  }
  if (inboundForm.protocol === 'vless' && !inboundForm.uuid.trim()) {
    inboundFormError.value = 'VLESS 需要 UUID'
    return null
  }
  if (
    (inboundForm.protocol === 'trojan' || inboundForm.protocol === 'shadowsocks') &&
    !inboundForm.password.trim()
  ) {
    inboundFormError.value = '請輸入或產生密碼'
    return null
  }
  if (inboundForm.network === 'ws' && !inboundForm.path.startsWith('/')) {
    inboundFormError.value = 'WebSocket path 必須以 / 開頭'
    return null
  }
  if (inboundForm.network === 'grpc' && !inboundForm.serviceName.trim()) {
    inboundFormError.value = 'gRPC 需要 serviceName'
    return null
  }
  if (inboundForm.security === 'tls' && (!inboundForm.certificateFile.trim() || !inboundForm.keyFile.trim())) {
    inboundFormError.value = 'TLS 需要憑證與私鑰路徑'
    return null
  }
  if (inboundForm.security === 'reality' && (!inboundForm.privateKey || !inboundForm.publicKey || !inboundForm.dest.trim())) {
    inboundFormError.value = 'REALITY 需要金鑰與 dest'
    return null
  }

  const settings: ProxyInboundSettings = {}
  if (inboundForm.protocol === 'vless') {
    settings.uuid = inboundForm.uuid.trim()
    if (showFlow.value && inboundForm.flow) settings.flow = inboundForm.flow
  }
  if (inboundForm.protocol === 'trojan') settings.password = inboundForm.password
  if (inboundForm.protocol === 'shadowsocks') {
    settings.method = inboundForm.method
    settings.password = inboundForm.password
  }
  if (isWebSocket.value) {
    settings.path = inboundForm.path.trim() || '/'
    if (inboundForm.host.trim()) settings.host = inboundForm.host.trim()
  }
  if (isGRPC.value) settings.service_name = inboundForm.serviceName.trim()
  if (isTLS.value) {
    settings.certificate_file = inboundForm.certificateFile.trim()
    settings.key_file = inboundForm.keyFile.trim()
    if (inboundForm.serverName.trim()) settings.server_name = inboundForm.serverName.trim()
  }
  if (isReality.value) {
    settings.private_key = inboundForm.privateKey
    settings.public_key = inboundForm.publicKey
    settings.short_id = inboundForm.shortID
    settings.dest = inboundForm.dest.trim()
  }
  return {
    remark: inboundForm.remark.trim(),
    port,
    protocol: inboundForm.protocol,
    network: inboundForm.network,
    security: inboundForm.security,
    settings,
  }
}

async function saveInbound(): Promise<void> {
  if (!props.node || inboundSaving.value) return
  const payload = buildInboundPayload()
  if (!payload) return
  inboundSaving.value = true
  inboundsError.value = ''
  inboundsWarning.value = ''
  try {
    const response = editingInbound.value
      ? await updateProxyInbound(props.node.id, editingInbound.value.id, payload)
      : await createProxyInbound(props.node.id, payload)
    if (editingInbound.value) {
      const index = inbounds.value.findIndex((item) => item.id === response.inbound.id)
      if (index >= 0) inbounds.value[index] = response.inbound
    } else {
      inbounds.value.push(response.inbound)
    }
    inbounds.value.sort((a, b) => a.port - b.port || a.id - b.id)
    inboundsWarning.value = response.synced
      ? ''
      : response.warning || '入站已儲存；Agent 離線時會在下一次回連後套用。'
    inboundDialogOpen.value = false
    editingInbound.value = null
    await refreshXraySnapshot()
    emit('refresh')
  } catch (value) {
    inboundFormError.value = messageOf(value)
  } finally {
    inboundSaving.value = false
  }
}

async function removeInbound(inbound: ProxyInbound): Promise<void> {
  if (!props.node || inboundDeletingID.value !== null) return
  if (!window.confirm(`刪除入站「${inbound.remark}」？Xray 會重新編譯並套用其餘規則。`)) return
  inboundDeletingID.value = inbound.id
  inboundsError.value = ''
  inboundsWarning.value = ''
  try {
    const response = await deleteProxyInbound(props.node.id, inbound.id)
    inbounds.value = inbounds.value.filter((item) => item.id !== inbound.id)
    inboundsWarning.value = response.synced
      ? ''
      : response.warning || '入站已刪除；Agent 離線時會在下一次回連後套用。'
    await refreshXraySnapshot()
    emit('refresh')
  } catch (value) {
    inboundsError.value = messageOf(value)
  } finally {
    inboundDeletingID.value = null
  }
}

async function refreshXraySnapshot(): Promise<void> {
  if (!props.node) return
  try {
    const response = await getXrayConfig(props.node.id)
    if (response) {
      xrayExists.value = true
      xrayConfigText.value = JSON.stringify(response.config, null, 2)
      xrayUpdatedAt.value = response.updated_at
    } else {
      xrayExists.value = false
      xrayConfigText.value = ''
      xrayUpdatedAt.value = null
    }
  } catch {
    xrayExists.value = false
    xrayConfigText.value = ''
    xrayUpdatedAt.value = null
  }
}

async function openShare(inbound: ProxyInbound): Promise<void> {
  if (!props.node) return
  shareInbound.value = inbound
  shareOpen.value = true
  shareLoading.value = true
  shareError.value = ''
  shareLink.value = ''
  shareQR.value = ''
  shareCopied.value = false
  try {
    const response = await getInboundShare(props.node.id, inbound.id)
    shareLink.value = response.link
    shareQR.value = await QRCode.toDataURL(response.link, {
      width: 320,
      margin: 1,
      errorCorrectionLevel: 'M',
      color: { dark: '#0b1119', light: '#ffffff' },
    })
  } catch (value) {
    shareError.value = messageOf(value)
  } finally {
    shareLoading.value = false
  }
}

function closeShare(): void {
  shareOpen.value = false
  shareInbound.value = null
  shareLink.value = ''
  shareQR.value = ''
  shareError.value = ''
  shareCopied.value = false
}

async function copyShareLink(): Promise<void> {
  if (!shareLink.value) return
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(shareLink.value)
    } else {
      const textarea = document.createElement('textarea')
      textarea.value = shareLink.value
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      textarea.remove()
    }
    shareCopied.value = true
    window.setTimeout(() => (shareCopied.value = false), 1800)
  } catch {
    shareError.value = '瀏覽器拒絕剪貼簿存取，請手動複製連結'
  }
}

function protocolLabel(protocol: InboundProtocol): string {
  switch (protocol) {
    case 'vless':
      return 'VLESS'
    case 'trojan':
      return 'Trojan'
    case 'shadowsocks':
      return 'Shadowsocks'
    default:
      return protocol
  }
}

function networkLabel(network: InboundNetwork): string {
  return network === 'tcp' ? 'TCP' : network === 'ws' ? 'WebSocket' : 'gRPC'
}

function securityLabel(security: InboundSecurity): string {
  return security === 'none' ? 'None' : security === 'tls' ? 'TLS' : 'REALITY'
}

function protocolClasses(protocol: InboundProtocol): string {
  if (protocol === 'vless') return 'border-violet-400/20 bg-violet-400/[0.06] text-violet-300'
  if (protocol === 'trojan') return 'border-sky-400/20 bg-sky-400/[0.06] text-sky-300'
  return 'border-amber-400/20 bg-amber-400/[0.06] text-amber-300'
}

function securityClasses(security: InboundSecurity): string {
  if (security === 'reality') return 'border-rose-400/20 bg-rose-400/[0.06] text-rose-300'
  if (security === 'tls') return 'border-emerald-400/20 bg-emerald-400/[0.06] text-emerald-300'
  return 'border-slate-500/20 bg-slate-500/[0.06] text-slate-400'
}

function inboundSummary(inbound: ProxyInbound): string {
  const settings = inbound.settings
  if (inbound.security === 'reality') return settings.dest || 'Reality'
  if (inbound.network === 'ws') return settings.path || '/'
  if (inbound.network === 'grpc') return settings.service_name || 'gRPC'
  if (inbound.protocol === 'shadowsocks') return settings.method || 'Shadowsocks'
  return inbound.security === 'tls' ? 'TLS' : 'Direct'
}

function close(): void {
  if (!saving.value && !xrayBusy.value && !inboundBusy.value) emit('close')
}

function onKeydown(event: KeyboardEvent): void {
  if (!props.open || event.key !== 'Escape') return
  if (replaceConfirmOpen.value) {
    if (!replacing.value) replaceConfirmOpen.value = false
    return
  }
  if (shareOpen.value) {
    closeShare()
    return
  }
  if (inboundDialogOpen.value) {
    closeInboundDialog()
    return
  }
  close()
}

watch(
  () => `${props.open ? 'open' : 'closed'}:${props.node?.id ?? ''}`,
  async () => {
    if (props.open) {
      details.value = props.node
      rules.value = []
      error.value = ''
      formError.value = ''
      warning.value = ''
      xrayConfigText.value = ''
      xrayExists.value = false
      xrayUpdatedAt.value = null
      xrayError.value = ''
      xrayWarning.value = ''
      inbounds.value = []
      inboundNodeHost.value = ''
      inboundsLoading.value = false
      inboundsError.value = ''
      inboundsWarning.value = ''
      inboundDialogOpen.value = false
      editingInbound.value = null
      shareOpen.value = false
      shareLink.value = ''
      shareQR.value = ''
      resetInboundForm()
      activeTab.value = isSimple.value ? 'basic' : 'port-forward'
      traffic.value = []
      trafficError.value = ''
      stopTrafficTimer()
      speedtestLoading.value = false
      speedtestError.value = ''
      speedtestResult.value = null
      cloudAutoHeal.value = false
      cloudProvider.value = ''
      cloudInstanceID.value = ''
      ddnsDomain.value = ''
      ddnsZoneID.value = ''
      ddnsRecordID.value = ''
      cloudSaving.value = false
      cloudError.value = ''
      cloudWarning.value = ''
      replaceConfirmOpen.value = false
      replacing.value = false
      replaceResult.value = null
      deleteConfirmOpen.value = false
      deleteConfirmText.value = ''
      deleteError.value = ''
      deleteLoading.value = false
      identityOpen.value = false
      identityName.value = ''
      identityAddress.value = ''
      identitySaving.value = false
      identityError.value = ''
      form.listen_port = 8080
      form.protocol = 'tcp'
      form.target_host = ''
      form.target_port = 80
      form.enabled = true
      previousOverflow = document.body.style.overflow
      document.body.style.overflow = 'hidden'
      await loadAll()
      await nextTick()
      hostInput.value?.focus()
    } else {
      requestSeq++
      details.value = null
      rules.value = []
      form.target_host = ''
      error.value = ''
      formError.value = ''
      warning.value = ''
      xrayConfigText.value = ''
      xrayExists.value = false
      xrayUpdatedAt.value = null
      xrayError.value = ''
      xrayWarning.value = ''
      xrayLoading.value = false
      inbounds.value = []
      inboundNodeHost.value = ''
      inboundsLoading.value = false
      inboundsError.value = ''
      inboundsWarning.value = ''
      inboundDialogOpen.value = false
      editingInbound.value = null
      shareOpen.value = false
      shareLink.value = ''
      shareQR.value = ''
      resetInboundForm()
      activeTab.value = isSimple.value ? 'basic' : 'port-forward'
      traffic.value = []
      trafficError.value = ''
      stopTrafficTimer()
      speedtestLoading.value = false
      speedtestError.value = ''
      speedtestResult.value = null
      cloudSaving.value = false
      cloudError.value = ''
      cloudWarning.value = ''
      replaceConfirmOpen.value = false
      replacing.value = false
      replaceResult.value = null
      deleteConfirmOpen.value = false
      deleteConfirmText.value = ''
      deleteError.value = ''
      deleteLoading.value = false
      identityOpen.value = false
      identityName.value = ''
      identityAddress.value = ''
      identitySaving.value = false
      identityError.value = ''
      document.body.style.overflow = previousOverflow
    }
  },
  { immediate: true },
)

watch(
  () => props.node,
  (node) => {
    if (node && details.value?.id === node.id) {
      details.value = { ...details.value, ...node }
    }
  },
)

onMounted(() => window.addEventListener('keydown', onKeydown))
onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  stopTrafficTimer()
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
      <div v-if="open && activeNode" class="fixed inset-0 z-50 overflow-y-auto">
        <div class="fixed inset-0 bg-black/75 backdrop-blur-sm" aria-hidden="true" @click="close" />
        <div class="relative flex min-h-full items-center justify-center p-4 sm:p-6">
          <div
            class="panel w-full max-w-4xl overflow-hidden border-white/[0.1] bg-[#0a0f18]"
            style="background-color: #0a0f18"
            role="dialog"
            aria-modal="true"
            :aria-label="`${activeNode.name} 節點詳情`"
          >
            <div class="h-px bg-gradient-to-r from-transparent via-emerald-400/70 to-transparent" />
            <header class="flex items-start justify-between gap-4 border-b border-white/[0.06] px-5 py-4 sm:px-6">
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <p class="label text-emerald-400/80">Node Detail</p>
                  <span
                    class="rounded-md border px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.12em]"
                    :class="statusClasses(activeNode.status)"
                  >
                    {{ statusLabel(activeNode.status) }}
                  </span>
                </div>
                <h2 class="mt-1.5 truncate text-lg font-medium text-white">{{ activeNode.name }}</h2>
                <p class="mt-0.5 truncate font-mono text-[10px] text-slate-500">{{ activeNode.id }}</p>
              </div>
              <div class="flex shrink-0 items-center gap-2">
              <button
                v-if="superAdmin"
                type="button"
                class="focus-ring rounded-lg border border-white/[0.08] px-2.5 py-1.5 text-[10px] text-slate-300 transition hover:bg-white/[0.06]"
                @click="openIdentityEditor"
              >
                名稱 / 位址
              </button>
              <button
                type="button"
                class="focus-ring grid h-8 w-8 shrink-0 place-items-center rounded-lg border border-white/[0.07] text-slate-400 transition hover:bg-white/[0.05] hover:text-white disabled:opacity-40"
                aria-label="關閉視窗"
                :disabled="saving || xrayBusy || inboundBusy"
                @click="close"
              >
                <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                  <path d="m6 6 12 12M18 6 6 18" stroke-linecap="round" />
                </svg>
              </button>
              </div>
            </header>

            <div class="max-h-[78vh] overflow-y-auto px-5 py-5 sm:px-6">
              <div v-if="initialLoading" class="flex items-center justify-center py-16">
                <svg viewBox="0 0 24 24" class="h-5 w-5 animate-spin text-emerald-300" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                  <path d="M21 12a9 9 0 1 1-6.2-8.56" stroke-linecap="round" />
                </svg>
                <span class="ml-3 text-sm text-slate-400">讀取節點資料…</span>
              </div>

              <template v-else>
                <div class="grid grid-cols-2 gap-px overflow-hidden rounded-xl border border-white/[0.06] bg-white/[0.05] sm:grid-cols-4">
                  <div class="bg-[#0b111b] px-4 py-3">
                    <p class="label">Address</p>
                    <p class="mt-1 truncate font-mono text-xs text-slate-300">{{ activeNode.address || activeNode.remote_ip || '—' }}</p>
                  </div>
                  <div class="bg-[#0b111b] px-4 py-3">
                    <p class="label">System</p>
                    <p class="mt-1 truncate font-mono text-xs text-slate-300">{{ activeNode.os || '—' }} / {{ activeNode.arch || '—' }}</p>
                  </div>
                  <div class="bg-[#0b111b] px-4 py-3">
                    <p class="label">Agent</p>
                    <p class="mt-1 truncate font-mono text-xs text-slate-300">v{{ activeNode.agent_version || '—' }}</p>
                  </div>
                  <div class="bg-[#0b111b] px-4 py-3">
                    <p class="label">Last Seen</p>
                    <p class="mt-1 truncate font-mono text-xs text-slate-300">{{ formatLastSeen(activeNode.last_seen) }}</p>
                  </div>
                </div>

                <div
                  v-if="!isSimple && !isNodeOnline"
                  class="mt-3 rounded-lg border border-amber-400/15 bg-amber-400/[0.05] px-3.5 py-3 text-[11px] leading-5 text-amber-100/80"
                >
                  節點目前離線。規則會先寫入 SQLite，並在 Agent 下次回連時同步。
                </div>
                <div
                  v-if="!isSimple && !supportsPortForward"
                  class="mt-3 rounded-lg border border-rose-500/15 bg-rose-500/[0.05] px-3.5 py-3 text-[11px] leading-5 text-rose-100/80"
                >
                  此 Agent 未回報 port_forward 插件；請先升級 Agent binary。
                </div>

                <div
                  v-if="warning"
                  class="mt-3 rounded-lg border border-amber-400/15 bg-amber-400/[0.05] px-3.5 py-3 text-[11px] leading-5 text-amber-100/80"
                  role="status"
                >
                  {{ warning }}
                </div>
                <div
                  v-if="error"
                  class="mt-3 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3.5 py-3 text-[11px] leading-5 text-rose-200"
                  role="alert"
                >
                  {{ error }}
                </div>

                <div v-if="!isSimple" class="mt-4 rounded-xl border border-white/[0.06] bg-white/[0.02] p-4">
                  <div class="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <p class="label text-amber-400/80">Bandwidth Probe</p>
                      <p class="mt-1 text-xs text-slate-300">節點極限上下行測速</p>
                      <p class="mt-1 text-[10px] leading-5 text-slate-600">
                        使用 Cloudflare 測速端點；執行約 10–20 秒，期間會占用節點頻寬。
                      </p>
                    </div>
                    <button
                      v-if="canManage"
                      type="button"
                      class="focus-ring inline-flex items-center gap-2 rounded-lg border border-amber-400/20 bg-amber-400/[0.07] px-3.5 py-2 text-[11px] font-semibold text-amber-200 transition hover:bg-amber-400/[0.12] disabled:cursor-not-allowed disabled:opacity-40"
                      :disabled="!isNodeOnline || !supportsSpeedtest || speedtestLoading"
                      :title="!supportsSpeedtest ? 'Agent 未回報 speedtest 插件' : ''"
                      @click="runNodeSpeedtest"
                    >
                      <svg
                        viewBox="0 0 24 24"
                        class="h-3.5 w-3.5"
                        :class="{ 'animate-spin': speedtestLoading }"
                        fill="none"
                        stroke="currentColor"
                        stroke-width="1.8"
                        aria-hidden="true"
                      >
                        <path
                          v-if="speedtestLoading"
                          d="M21 12a9 9 0 1 1-6.2-8.56"
                          stroke-linecap="round"
                        />
                        <path
                          v-else
                          d="M4 18a8 8 0 1 1 16 0M12 14l4-3"
                          stroke-linecap="round"
                        />
                      </svg>
                      {{ speedtestLoading ? '測速中…' : '一鍵測速' }}
                    </button>
                  </div>

                  <div
                    v-if="speedtestError"
                    class="mt-3 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-[11px] leading-5 text-rose-200"
                    role="alert"
                  >
                    {{ speedtestError }}
                  </div>
                  <div v-if="speedtestLoading" class="mt-3 grid grid-cols-3 gap-2 animate-pulse">
                    <div v-for="item in 3" :key="item" class="h-16 rounded-lg bg-white/[0.04]"></div>
                  </div>
                  <div v-else-if="speedtestResult" class="mt-3 grid grid-cols-3 gap-2">
                    <div class="rounded-lg border border-white/[0.05] bg-[#0a1019] px-3 py-2.5">
                      <p class="font-mono text-[9px] uppercase tracking-[0.12em] text-slate-600">Ping</p>
                      <p class="mt-1 font-mono text-lg font-semibold tabular-nums text-white">
                        {{ speedtestResult.ping_ms.toFixed(1) }}<span class="ml-1 text-[10px] text-slate-500">ms</span>
                      </p>
                    </div>
                    <div class="rounded-lg border border-emerald-400/10 bg-emerald-400/[0.035] px-3 py-2.5">
                      <p class="font-mono text-[9px] uppercase tracking-[0.12em] text-emerald-400/70">Download</p>
                      <p class="mt-1 font-mono text-lg font-semibold tabular-nums text-emerald-300">
                        {{ speedtestResult.download_mbps.toFixed(1) }}<span class="ml-1 text-[10px] text-emerald-500/70">Mbps</span>
                      </p>
                    </div>
                    <div class="rounded-lg border border-sky-400/10 bg-sky-400/[0.035] px-3 py-2.5">
                      <p class="font-mono text-[9px] uppercase tracking-[0.12em] text-sky-400/70">Upload</p>
                      <p class="mt-1 font-mono text-lg font-semibold tabular-nums text-sky-300">
                        {{ speedtestResult.upload_mbps.toFixed(1) }}<span class="ml-1 text-[10px] text-sky-500/70">Mbps</span>
                      </p>
                    </div>
                  </div>
                  <p
                    v-if="speedtestResult"
                    class="mt-2 font-mono text-[9px] text-slate-600"
                  >
                    {{ speedtestResult.server }} · {{ (speedtestResult.duration_ms / 1000).toFixed(1) }}s
                  </p>
                </div>

                <div class="mt-4 flex gap-1 rounded-xl border border-white/[0.06] bg-white/[0.02] p-1">
                  <button
                    v-if="isSimple"
                    type="button"
                    class="focus-ring flex flex-1 items-center justify-center gap-2 rounded-lg px-3 py-2 text-[11px] font-medium transition"
                    :class="activeTab === 'basic' ? 'bg-emerald-400/10 text-emerald-200 shadow-sm' : 'text-slate-500 hover:text-slate-300'"
                    @click="selectTab('basic')"
                  >
                    基礎狀態
                  </button>
                  <button
                    v-if="isSimple"
                    type="button"
                    class="focus-ring flex flex-1 items-center justify-center gap-2 rounded-lg px-3 py-2 text-[11px] font-medium transition"
                    :class="activeTab === 'subscriptions' ? 'bg-cyan-400/10 text-cyan-200 shadow-sm' : 'text-slate-500 hover:text-slate-300'"
                    @click="selectTab('subscriptions')"
                  >
                    節點訂閱連結
                    <span class="rounded bg-white/[0.06] px-1.5 py-0.5 font-mono text-[9px] text-slate-400">{{ inbounds.length }}</span>
                  </button>
                  <template v-if="!isSimple">
                  <button
                    type="button"
                    class="focus-ring flex flex-1 items-center justify-center gap-2 rounded-lg px-3 py-2 text-[11px] font-medium transition"
                    :class="
                      activeTab === 'port-forward'
                        ? 'bg-white/[0.07] text-white shadow-sm'
                        : 'text-slate-500 hover:text-slate-300'
                    "
                    @click="selectTab('port-forward')"
                  >
                    端口轉發
                    <span class="rounded bg-white/[0.06] px-1.5 py-0.5 font-mono text-[9px] text-slate-400">{{ rules.length }}</span>
                  </button>
                  <button
                    type="button"
                    class="focus-ring flex flex-1 items-center justify-center gap-2 rounded-lg px-3 py-2 text-[11px] font-medium transition"
                    :class="
                      activeTab === 'inbounds'
                        ? 'bg-white/[0.07] text-white shadow-sm'
                        : 'text-slate-500 hover:text-slate-300'
                    "
                    @click="selectTab('inbounds')"
                  >
                    入站規則
                    <span class="rounded bg-white/[0.06] px-1.5 py-0.5 font-mono text-[9px] text-slate-400">{{ inbounds.length }}</span>
                  </button>
                  <button
                    type="button"
                    class="focus-ring flex flex-1 items-center justify-center gap-2 rounded-lg px-3 py-2 text-[11px] font-medium transition"
                    :class="
                      activeTab === 'xray'
                        ? 'bg-white/[0.07] text-white shadow-sm'
                        : 'text-slate-500 hover:text-slate-300'
                    "
                    @click="selectTab('xray')"
                  >
                    進階 JSON
                    <span
                      class="h-1.5 w-1.5 rounded-full"
                      :class="xrayExists ? 'bg-violet-400' : 'bg-slate-700'"
                    ></span>
                  </button>
                  <button
                    type="button"
                    class="focus-ring flex flex-1 items-center justify-center gap-2 rounded-lg px-3 py-2 text-[11px] font-medium transition"
                    :class="
                      activeTab === 'traffic'
                        ? 'bg-white/[0.07] text-white shadow-sm'
                        : 'text-slate-500 hover:text-slate-300'
                    "
                    @click="selectTab('traffic')"
                  >
                    用戶流量
                    <span class="rounded bg-white/[0.06] px-1.5 py-0.5 font-mono text-[9px] text-slate-400">{{ traffic.length }}</span>
                  </button>
                  <button
                    type="button"
                    class="focus-ring flex flex-1 items-center justify-center gap-2 rounded-lg px-3 py-2 text-[11px] font-medium transition"
                    :class="
                      activeTab === 'cloud'
                        ? 'bg-white/[0.07] text-white shadow-sm'
                        : 'text-slate-500 hover:text-slate-300'
                    "
                    @click="selectTab('cloud')"
                  >
                    雲端主機控管
                    <span
                      class="h-1.5 w-1.5 rounded-full"
                      :class="cloudAutoHeal ? 'bg-emerald-400' : 'bg-slate-700'"
                    ></span>
                  </button>
                  </template>
                </div>

                <section v-if="isSimple" v-show="activeTab === 'basic'" class="mt-4">
                  <div class="rounded-2xl border border-white/[0.07] bg-white/[0.02] p-5">
                    <div class="flex flex-wrap items-start justify-between gap-3">
                      <div>
                        <p class="label text-emerald-400/80">Node Overview</p>
                        <h3 class="mt-1 text-lg font-semibold text-white">{{ activeNode?.name || activeNode?.id }}</h3>
                        <p class="mt-1 font-mono text-[10px] text-slate-500">{{ activeNode?.id }} · {{ activeNode?.address || activeNode?.remote_ip || '—' }}</p>
                      </div>
                      <span class="rounded-full border border-emerald-400/20 bg-emerald-400/[0.06] px-3 py-1 font-mono text-[10px] uppercase tracking-[0.1em] text-emerald-300">
                        {{ activeNode?.status || 'unknown' }}
                      </span>
                    </div>
                    <div class="mt-5 grid grid-cols-2 gap-3 sm:grid-cols-4">
                      <div class="rounded-xl border border-white/[0.06] bg-black/20 p-3">
                        <p class="label">CPU</p>
                        <p class="mt-1 font-mono text-xl text-emerald-300">{{ formatPercent(activeNode?.cpu_percent) }}</p>
                      </div>
                      <div class="rounded-xl border border-white/[0.06] bg-black/20 p-3">
                        <p class="label">RAM</p>
                        <p class="mt-1 font-mono text-xl text-sky-300">{{ formatPercent(activeNode?.memory_percent) }}</p>
                      </div>
                      <div class="rounded-xl border border-white/[0.06] bg-black/20 p-3">
                        <p class="label">Agent</p>
                        <p class="mt-1 font-mono text-sm text-slate-200">v{{ activeNode?.agent_version || '—' }}</p>
                      </div>
                      <div class="rounded-xl border border-white/[0.06] bg-black/20 p-3">
                        <p class="label">Uptime</p>
                        <p class="mt-1 font-mono text-sm text-slate-200">{{ formatUptime(activeNode?.online_seconds || 0) }}</p>
                      </div>
                    </div>
                    <div class="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-white/[0.06] pt-4">
                      <div class="flex flex-wrap gap-1.5">
                        <span v-for="tag in activeNode?.tags || []" :key="tag" class="rounded border border-cyan-400/10 bg-cyan-400/[0.04] px-2 py-1 font-mono text-[9px] text-cyan-300/80">{{ tag }}</span>
                        <span v-if="!activeNode?.tags?.length" class="font-mono text-[10px] text-slate-600">NO TAGS</span>
                      </div>
                      <div class="flex items-center gap-2">
                        <button v-if="superAdmin" type="button" class="focus-ring rounded-lg border border-rose-400/20 px-3 py-2 text-[11px] text-rose-300 transition hover:bg-rose-400/[0.08]" @click="openDeleteConfirm">
                          移除節點
                        </button>
                        <button type="button" class="focus-ring rounded-lg bg-cyan-400 px-3.5 py-2 text-[11px] font-semibold text-cyan-950" @click="selectTab('subscriptions')">
                          查看訂閱連結
                        </button>
                      </div>
                    </div>
                  </div>
                </section>

                <section v-if="isSimple" v-show="activeTab === 'subscriptions'" class="mt-4">
                  <div class="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <p class="label text-cyan-400/80">Subscription Links</p>
                      <h3 class="mt-1 text-sm font-medium text-white">節點訂閱連結</h3>
                      <p class="mt-1 text-[10px] text-slate-500">分享連結 / QR 由既有 Xray inbound 產生；金鑰已自動管理。</p>
                    </div>
                    <button v-if="canManage && supportsXray" type="button" class="focus-ring rounded-lg bg-cyan-400 px-3 py-2 text-[11px] font-semibold text-cyan-950" @click="openInboundDialog()">
                      + 新增協議
                    </button>
                  </div>
                  <div v-if="inbounds.length === 0" class="mt-4 rounded-xl border border-dashed border-white/[0.08] p-8 text-center">
                    <p class="text-sm text-slate-300">尚未建立訂閱協議</p>
                    <p class="mt-1 text-[11px] text-slate-600">建立 VLESS/Trojan/Shadowsocks 後，這裡會出現分享連結與 QR。</p>
                  </div>
                  <div v-else class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <div v-for="inbound in inbounds" :key="inbound.id" class="rounded-xl border border-white/[0.07] bg-white/[0.02] p-4">
                      <div class="flex items-start justify-between gap-2">
                        <div>
                          <p class="text-sm font-medium text-white">{{ inbound.remark || 'Unnamed' }}</p>
                          <p class="mt-1 font-mono text-[10px] text-slate-500">{{ inbound.protocol }} · {{ inbound.network }} · :{{ inbound.port }}</p>
                        </div>
                        <span class="rounded border border-cyan-400/15 bg-cyan-400/[0.05] px-1.5 py-0.5 font-mono text-[9px] text-cyan-300">{{ inbound.security }}</span>
                      </div>
                      <div class="mt-4 flex items-center justify-between gap-2">
                        <p class="truncate font-mono text-[9px] text-slate-600">{{ inboundNodeHost || '—' }}</p>
                        <div class="flex shrink-0 items-center gap-1.5">
                          <button v-if="canManage" type="button" class="focus-ring rounded-lg border border-rose-400/20 px-2.5 py-1.5 text-[10px] text-rose-300 hover:bg-rose-400/[0.08] disabled:opacity-40" :disabled="inboundDeletingID !== null" @click="removeInbound(inbound)">
                            {{ inboundDeletingID === inbound.id ? '…' : '刪除' }}
                          </button>
                          <button type="button" class="focus-ring rounded-lg border border-white/[0.1] px-3 py-1.5 text-[10px] text-slate-200 hover:bg-white/[0.06]" @click="openShare(inbound)">
                            分享 / QR
                          </button>
                        </div>
                      </div>
                    </div>
                  </div>
                </section>

                <section v-show="activeTab === 'port-forward'" class="mt-4">
                  <div class="flex items-center justify-between gap-3">
                    <div>
                      <p class="label text-emerald-400/80">TCP Port Forwarding</p>
                      <h3 class="mt-1 text-sm font-medium text-white">端口轉發規則</h3>
                    </div>
                    <p class="hidden font-mono text-[10px] text-slate-500 sm:block">
                      io.Copy / NO IPTABLES
                    </p>
                  </div>

                  <form v-if="canManage" class="mt-3 rounded-xl border border-white/[0.06] bg-white/[0.02] p-4" @submit.prevent="addRule">
                    <div class="grid grid-cols-1 gap-3 sm:grid-cols-[.7fr_.8fr_1.4fr_.8fr_auto] sm:items-end">
                      <div>
                        <label for="forward-listen-port" class="label">本機端口</label>
                        <input
                          id="forward-listen-port"
                          v-model.number="form.listen_port"
                          type="number"
                          min="1"
                          max="65535"
                          inputmode="numeric"
                          class="focus-ring mt-2 w-full rounded-lg border border-white/[0.08] bg-black/25 px-3 py-2 font-mono text-xs text-slate-100 transition focus:border-emerald-400/40 disabled:opacity-50"
                          :disabled="saving"
                        />
                      </div>
                      <div>
                        <label for="forward-protocol" class="label">協議</label>
                        <select
                          id="forward-protocol"
                          v-model="form.protocol"
                          class="focus-ring mt-2 w-full rounded-lg border border-white/[0.08] bg-[#0b1119] px-2 py-2 font-mono text-xs text-slate-200 outline-none focus:border-emerald-400/40 disabled:opacity-50"
                          :disabled="saving"
                        >
                          <option value="tcp">TCP</option>
                          <option value="udp">UDP</option>
                        </select>
                      </div>
                      <div>
                        <label for="forward-target-host" class="label">目標主機 / 域名</label>
                        <input
                          id="forward-target-host"
                          ref="hostInput"
                          v-model="form.target_host"
                          type="text"
                          autocomplete="off"
                          autocapitalize="off"
                          spellcheck="false"
                          placeholder="10.0.0.8 或 backend.internal"
                          class="focus-ring mt-2 w-full rounded-lg border border-white/[0.08] bg-black/25 px-3 py-2 font-mono text-xs text-slate-100 placeholder:text-slate-600 transition focus:border-emerald-400/40 disabled:opacity-50"
                          :disabled="saving"
                        />
                      </div>
                      <div>
                        <label for="forward-target-port" class="label">目標端口</label>
                        <input
                          id="forward-target-port"
                          v-model.number="form.target_port"
                          type="number"
                          min="1"
                          max="65535"
                          inputmode="numeric"
                          class="focus-ring mt-2 w-full rounded-lg border border-white/[0.08] bg-black/25 px-3 py-2 font-mono text-xs text-slate-100 transition focus:border-emerald-400/40 disabled:opacity-50"
                          :disabled="saving"
                        />
                      </div>
                      <button
                        type="submit"
                        class="focus-ring inline-flex items-center justify-center gap-2 rounded-lg bg-emerald-400 px-3.5 py-2 text-xs font-semibold text-emerald-950 transition hover:bg-emerald-300 disabled:cursor-wait disabled:opacity-60"
                        :disabled="saving"
                      >
                        <svg v-if="saving" viewBox="0 0 24 24" class="h-3.5 w-3.5 animate-spin" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                          <path d="M21 12a9 9 0 1 1-6.2-8.56" stroke-linecap="round" />
                        </svg>
                        <svg v-else viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                          <path d="M12 5v14M5 12h14" stroke-linecap="round" />
                        </svg>
                        新增
                      </button>
                    </div>
                    <div class="mt-3 flex flex-wrap items-center justify-between gap-3">
                      <label class="inline-flex items-center gap-2 text-[11px] text-slate-400">
                        <input
                          v-model="form.enabled"
                          type="checkbox"
                          class="h-3.5 w-3.5 rounded border-white/20 bg-black/30 text-emerald-400 focus:ring-emerald-400/40"
                          :disabled="saving"
                        />
                        建立後立即啟用
                      </label>
                      <p v-if="formError" class="text-[11px] text-rose-300" role="alert">{{ formError }}</p>
                    </div>
                  </form>

                  <div class="mt-3 overflow-hidden rounded-xl border border-white/[0.06]">
                    <div class="overflow-x-auto">
                      <table class="w-full min-w-[560px] text-left">
                        <thead>
                          <tr class="border-b border-white/[0.05] text-[10px] uppercase tracking-[0.14em] text-slate-500">
                            <th class="px-4 py-3 font-medium">本機端口</th>
                            <th class="px-4 py-3 font-medium">協議</th>
                            <th class="px-4 py-3 font-medium">轉發目標</th>
                            <th class="px-4 py-3 font-medium">狀態</th>
                            <th class="px-4 py-3 text-right font-medium">操作</th>
                          </tr>
                        </thead>
                        <tbody class="divide-y divide-white/[0.045]">
                          <tr v-if="rules.length === 0">
                            <td colspan="5" class="px-4 py-10 text-center text-xs text-slate-500">
                              尚無端口轉發規則
                            </td>
                          </tr>
                          <tr v-for="rule in rules" :key="rule.rule_id" class="hover:bg-white/[0.02]">
                            <td class="px-4 py-3">
                              <span class="font-mono text-xs text-slate-200">{{ rule.listen_port }}</span>
                              <p class="mt-0.5 max-w-[120px] truncate font-mono text-[9px] text-slate-600">{{ rule.rule_id }}</p>
                            </td>
                            <td class="px-4 py-3">
                              <span class="inline-block rounded border px-1.5 py-0.5 font-mono text-[9px] uppercase" :class="rule.protocol === 'udp' ? 'border-sky-400/20 bg-sky-400/[0.06] text-sky-300' : 'border-slate-500/20 bg-slate-500/[0.06] text-slate-400'">
                                {{ rule.protocol || 'tcp' }}
                              </span>
                            </td>
                            <td class="px-4 py-3">
                              <span class="font-mono text-xs text-slate-300">{{ formatTarget(rule) }}</span>
                            </td>
                            <td class="px-4 py-3">
                              <span
                                class="inline-flex items-center gap-1.5 rounded-md border px-2 py-1 font-mono text-[9px] uppercase tracking-[0.1em]"
                                :class="
                                  rule.enabled
                                    ? 'border-emerald-400/15 bg-emerald-400/[0.06] text-emerald-300'
                                    : 'border-slate-500/20 bg-slate-500/[0.06] text-slate-400'
                                "
                              >
                                <span class="h-1 w-1 rounded-full" :class="rule.enabled ? 'bg-emerald-400' : 'bg-slate-500'" />
                                {{ rule.enabled ? 'Enabled' : 'Disabled' }}
                              </span>
                            </td>
                            <td class="px-4 py-3">
                              <div v-if="canManage" class="flex justify-end gap-2">
                                <button
                                  type="button"
                                  class="focus-ring rounded-md border border-white/[0.08] px-2.5 py-1.5 text-[10px] font-medium text-slate-300 transition hover:bg-white/[0.05] disabled:opacity-40"
                                  :disabled="busyRule !== null"
                                  @click="toggleRule(rule)"
                                >
                                  {{ rule.enabled ? '停用' : '啟用' }}
                                </button>
                                <button
                                  type="button"
                                  class="focus-ring rounded-md border border-rose-400/20 px-2.5 py-1.5 text-[10px] font-medium text-rose-300 transition hover:bg-rose-400/[0.08] disabled:opacity-40"
                                  :disabled="busyRule !== null"
                                  @click="removeRule(rule)"
                                >
                                  刪除
                                </button>
                              </div>
                            </td>
                          </tr>
                        </tbody>
                      </table>
                    </div>
                  </div>
                  <p class="mt-2 text-[10px] leading-5 text-slate-600">
                    規則狀態為控制面期望值；實際 Listener 由 Agent 套用並回報。停用會關閉舊 Listener 與現有連線。
                  </p>
                </section>

                <section v-show="activeTab === 'inbounds'" class="mt-4">
                  <div class="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <p class="label text-violet-400/80">Visual Proxy Builder</p>
                      <h3 class="mt-1 text-sm font-medium text-white">入站規則</h3>
                      <p class="mt-1 text-[10px] text-slate-600">
                        結構化欄位由後端編譯成 Xray JSON；每次儲存都會重新生成並下發。
                      </p>
                    </div>
                    <button
                      v-if="canManage"
                      type="button"
                      class="focus-ring inline-flex items-center gap-2 rounded-lg bg-violet-400 px-3.5 py-2 text-[11px] font-semibold text-violet-950 transition hover:bg-violet-300 disabled:cursor-not-allowed disabled:opacity-40"
                      :disabled="!supportsXray || inboundsLoading"
                      @click="openInboundDialog()"
                    >
                      <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                        <path d="M12 5v14M5 12h14" stroke-linecap="round" />
                      </svg>
                      新增入站
                    </button>
                  </div>

                  <div
                    v-if="!supportsXray"
                    class="mt-3 rounded-lg border border-rose-500/15 bg-rose-500/[0.05] px-3.5 py-3 text-[11px] leading-5 text-rose-100/80"
                  >
                    此 Agent 未回報 xray 插件；請先升級 Agent binary。
                  </div>
                  <div
                    v-if="inboundsWarning"
                    class="mt-3 rounded-lg border border-amber-400/15 bg-amber-400/[0.05] px-3.5 py-3 text-[11px] leading-5 text-amber-100/80"
                    role="status"
                  >
                    {{ inboundsWarning }}
                  </div>
                  <div
                    v-if="inboundsError"
                    class="mt-3 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3.5 py-3 text-[11px] leading-5 text-rose-200"
                    role="alert"
                  >
                    {{ inboundsError }}
                  </div>

                  <div class="mt-3 overflow-hidden rounded-xl border border-white/[0.06] bg-white/[0.02]">
                    <div v-if="inboundsLoading && inbounds.length === 0" class="animate-pulse divide-y divide-white/[0.05]">
                      <div v-for="row in 3" :key="row" class="flex items-center gap-4 px-4 py-4">
                        <div class="h-3 w-32 rounded bg-slate-800/80" />
                        <div class="ml-auto h-3 w-16 rounded bg-slate-800/80" />
                        <div class="h-3 w-20 rounded bg-slate-800/80" />
                      </div>
                    </div>
                    <div v-else-if="inbounds.length === 0" class="px-5 py-14 text-center">
                      <div class="mx-auto grid h-11 w-11 place-items-center rounded-full border border-violet-400/15 bg-violet-400/[0.05]">
                        <svg viewBox="0 0 24 24" class="h-5 w-5 text-violet-300/70" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                          <path d="M4 7h16M4 12h10M4 17h7M17 15l3 3-3 3" stroke-linecap="round" stroke-linejoin="round" />
                        </svg>
                      </div>
                      <p class="mt-3 text-xs font-medium text-slate-300">尚無入站規則</p>
                      <p class="mx-auto mt-1.5 max-w-sm text-[10px] leading-5 text-slate-600">
                        建立 VLESS / Trojan / Shadowsocks 入站後，系統會自動加入 Stats API、routing 與 freedom/blackhole outbounds。
                      </p>
                      <button
                        type="button"
                        class="focus-ring mt-4 rounded-lg border border-violet-400/20 bg-violet-400/[0.07] px-3 py-2 text-[11px] font-medium text-violet-200 transition hover:bg-violet-400/[0.12] disabled:opacity-40"
                        :disabled="!supportsXray"
                        @click="openInboundDialog()"
                      >
                        建立第一個入站
                      </button>
                    </div>
                    <div v-else class="overflow-x-auto">
                      <table class="w-full min-w-[760px] text-left">
                        <thead>
                          <tr class="border-b border-white/[0.05] text-[9px] uppercase tracking-[0.14em] text-slate-500">
                            <th class="px-4 py-3 font-medium">備註 / 協議</th>
                            <th class="px-4 py-3 font-medium">端口</th>
                            <th class="px-4 py-3 font-medium">傳輸</th>
                            <th class="px-4 py-3 font-medium">安全</th>
                            <th class="px-4 py-3 font-medium">參數</th>
                            <th class="px-4 py-3 text-right font-medium">操作</th>
                          </tr>
                        </thead>
                        <tbody class="divide-y divide-white/[0.045]">
                          <tr v-for="inbound in inbounds" :key="inbound.id" class="align-top transition-colors hover:bg-white/[0.02]">
                            <td class="px-4 py-3">
                              <p class="max-w-[170px] truncate text-xs font-medium text-slate-200">{{ inbound.remark }}</p>
                              <span class="mt-1 inline-block rounded border px-1.5 py-0.5 font-mono text-[9px]" :class="protocolClasses(inbound.protocol)">
                                {{ protocolLabel(inbound.protocol) }}
                              </span>
                            </td>
                            <td class="px-4 py-3 font-mono text-xs text-slate-300">
                              {{ inbound.port }}
                              <p class="mt-0.5 max-w-[150px] truncate font-mono text-[9px] text-slate-600">{{ inboundNodeHost || 'no host' }}</p>
                            </td>
                            <td class="px-4 py-3 text-[11px] text-slate-400">{{ networkLabel(inbound.network) }}</td>
                            <td class="px-4 py-3">
                              <span class="inline-block rounded border px-1.5 py-0.5 font-mono text-[9px]" :class="securityClasses(inbound.security)">
                                {{ securityLabel(inbound.security) }}
                              </span>
                            </td>
                            <td class="max-w-[170px] px-4 py-3">
                              <p class="truncate font-mono text-[10px] text-slate-500" :title="inboundSummary(inbound)">
                                {{ inboundSummary(inbound) }}
                              </p>
                            </td>
                            <td class="px-4 py-3">
                              <div class="flex justify-end gap-1">
                                <button
                                  type="button"
                                  class="focus-ring grid h-7 w-7 place-items-center rounded-md border border-emerald-400/15 text-emerald-300 transition hover:bg-emerald-400/[0.08]"
                                  title="分享連結 / 二維碼"
                                  aria-label="分享連結"
                                  @click="openShare(inbound)"
                                >
                                  <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true">
                                    <path d="M8.5 13.5l7-4M8.5 10.5l7 4" stroke-linecap="round" />
                                    <circle cx="6" cy="12" r="2.3" />
                                    <circle cx="18" cy="7" r="2.3" />
                                    <circle cx="18" cy="17" r="2.3" />
                                  </svg>
                                </button>
                                <button
                                  type="button"
                                  class="focus-ring rounded-md border border-white/[0.08] px-2.5 py-1.5 text-[10px] font-medium text-slate-300 transition hover:bg-white/[0.05]"
                                  @click="openInboundDialog(inbound)"
                                >
                                  編輯
                                </button>
                                <button
                                  type="button"
                                  class="focus-ring rounded-md border border-rose-400/20 px-2.5 py-1.5 text-[10px] font-medium text-rose-300 transition hover:bg-rose-400/[0.08] disabled:opacity-40"
                                  :disabled="inboundDeletingID !== null"
                                  @click="removeInbound(inbound)"
                                >
                                  {{ inboundDeletingID === inbound.id ? '…' : '刪除' }}
                                </button>
                              </div>
                            </td>
                          </tr>
                        </tbody>
                      </table>
                    </div>
                  </div>
                  <p class="mt-2 text-[10px] leading-5 text-slate-600">
                    生成器自動加入 <span class="font-mono text-slate-500">stats / policy / Stats API (127.0.0.1:10085)</span>、私有網段與廣告攔截 routing，以及 freedom/blackhole outbounds。
                  </p>
                </section>

                <section v-show="activeTab === 'xray'" class="mt-4">
                  <div class="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <p class="label text-violet-400/80">Advanced JSON</p>
                      <h3 class="mt-1 text-sm font-medium text-white">進階 Xray JSON</h3>
                    </div>
                    <div class="flex items-center gap-2">
                      <span
                        class="rounded-md border px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.12em]"
                        :class="
                          xrayExists
                            ? 'border-violet-400/20 bg-violet-400/[0.07] text-violet-300'
                            : 'border-slate-500/20 bg-slate-500/[0.06] text-slate-400'
                        "
                      >
                        {{ xrayExists ? 'Configured' : 'Not configured' }}
                      </span>
                      <span v-if="xrayUpdatedAt" class="font-mono text-[9px] text-slate-600">
                        {{ formatLastSeen(xrayUpdatedAt) }}
                      </span>
                    </div>
                  </div>

                  <div class="mt-3 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-violet-400/15 bg-violet-400/[0.03] px-3.5 py-3">
                    <div class="font-mono text-[10px] text-slate-400">
                      <span class="text-slate-500">PROCESS</span>
                      <span class="ml-2" :class="xrayStatus?.running ? 'text-emerald-300' : 'text-rose-300'">{{ xrayStatus?.running ? 'RUNNING' : 'STOPPED / UNKNOWN' }}</span>
                      <span v-if="xrayStatus?.pid" class="ml-2 text-slate-500">PID {{ xrayStatus.pid }}</span>
                    </div>
                    <div class="flex items-center gap-2">
                      <button type="button" class="focus-ring rounded border border-white/[0.1] px-2.5 py-1 text-[10px] text-slate-300 disabled:opacity-40" :disabled="xrayStatusLoading" @click="loadXrayStatus">{{ xrayStatusLoading ? '查詢中…' : '重新整理狀態' }}</button>
                      <button v-if="superAdmin" type="button" class="focus-ring rounded border border-amber-400/20 px-2.5 py-1 text-[10px] text-amber-300 disabled:opacity-40" :disabled="xrayRestarting" @click="restartXrayProcess">{{ xrayRestarting ? '重啟中…' : '背景重啟 Xray' }}</button>
                    </div>
                  </div>

                  <div
                    v-if="!supportsXray"
                    class="mt-3 rounded-lg border border-rose-500/15 bg-rose-500/[0.05] px-3.5 py-3 text-[11px] leading-5 text-rose-100/80"
                  >
                    此 Agent 未回報 xray 插件；請先升級 Agent binary。
                  </div>
                  <div class="mt-3 rounded-lg border border-amber-400/15 bg-amber-400/[0.05] px-3.5 py-3 text-[11px] leading-5 text-amber-100/80">
                    此頁是 legacy 進階編輯器。使用「入站規則」後，結構化資料是唯一來源；手動 JSON 會在下次入站變更時被生成器覆寫。
                  </div>
                  <div
                    v-if="xrayWarning"
                    class="mt-3 rounded-lg border border-amber-400/15 bg-amber-400/[0.05] px-3.5 py-3 text-[11px] leading-5 text-amber-100/80"
                    role="status"
                  >
                    {{ xrayWarning }}
                  </div>
                  <div
                    v-if="xrayError"
                    class="mt-3 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3.5 py-3 text-[11px] leading-5 text-rose-200"
                    role="alert"
                  >
                    {{ xrayError }}
                  </div>

                  <div class="mt-3 rounded-xl border border-white/[0.06] bg-white/[0.02] p-4">
                    <textarea
                      v-model="xrayConfigText"
                      rows="12"
                      spellcheck="false"
                      autocomplete="off"
                      placeholder='{"log":{"loglevel":"warning"},"inbounds":[],"outbounds":[]}'
                      class="focus-ring w-full resize-y rounded-lg border border-white/[0.08] bg-black/30 p-3 font-mono text-[11px] leading-5 text-slate-200 placeholder:text-slate-600 transition focus:border-violet-400/40 disabled:opacity-50"
                      :disabled="xrayLoading || xrayBusy"
                    />
                    <div class="mt-3 flex flex-wrap items-center justify-between gap-2">
                      <p class="text-[10px] leading-5 text-slate-600">
                        完整 Xray-core JSON；儲存後會寫入 Agent 並平滑重啟進程。配置可能含敏感資訊，不會出現在日誌。
                      </p>
                      <div class="flex items-center gap-2">
                        <button
                          type="button"
                          class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-[11px] font-medium text-slate-400 transition hover:bg-white/[0.05] hover:text-white disabled:opacity-40"
                          :disabled="xrayLoading || xrayBusy || !xrayConfigText.trim()"
                          @click="formatXrayJSON"
                        >
                          格式化 JSON
                        </button>
                        <button
                          v-if="canManage"
                          type="button"
                          class="focus-ring inline-flex items-center gap-2 rounded-lg bg-violet-400 px-3 py-2 text-[11px] font-semibold text-violet-950 transition hover:bg-violet-300 disabled:cursor-wait disabled:opacity-60"
                          :disabled="xrayLoading || xrayBusy || !xrayConfigText.trim()"
                          @click="saveXray"
                        >
                          <svg v-if="xraySaving" viewBox="0 0 24 24" class="h-3.5 w-3.5 animate-spin" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                            <path d="M21 12a9 9 0 1 1-6.2-8.56" stroke-linecap="round" />
                          </svg>
                          {{ xraySaving ? '套用中…' : '儲存並套用' }}
                        </button>
                        <button
                          v-if="canManage && xrayExists"
                          type="button"
                          class="focus-ring rounded-lg border border-rose-400/20 px-3 py-2 text-[11px] font-medium text-rose-300 transition hover:bg-rose-400/[0.08] disabled:opacity-40"
                          :disabled="xrayBusy"
                          @click="removeXray"
                        >
                          刪除
                        </button>
                      </div>
                    </div>
                  </div>
                </section>

                <section v-show="activeTab === 'traffic'" class="mt-4">
                  <div class="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <p class="label text-cyan-400/80">Xray Traffic Accounting</p>
                      <h3 class="mt-1 text-sm font-medium text-white">用戶流量</h3>
                    </div>
                    <button
                      type="button"
                      class="focus-ring inline-flex items-center gap-2 rounded-lg border border-white/[0.08] px-3 py-2 text-[11px] font-medium text-slate-400 transition hover:bg-white/[0.05] hover:text-white disabled:opacity-40"
                      :disabled="trafficLoading"
                      @click="loadTraffic"
                    >
                      <svg
                        viewBox="0 0 24 24"
                        class="h-3.5 w-3.5"
                        :class="{ 'animate-spin': trafficLoading }"
                        fill="none"
                        stroke="currentColor"
                        stroke-width="1.8"
                        aria-hidden="true"
                      >
                        <path d="M20 11a8 8 0 1 0-2.34 5.66M20 4v7h-7" stroke-linecap="round" stroke-linejoin="round" />
                      </svg>
                      重新整理
                    </button>
                  </div>

                  <p class="mt-2 text-[10px] leading-5 text-slate-600">
                    累計值來自 Agent 每 30 秒查詢 Xray StatsService；節點需在 Xray 配置中開啟 stats、policy 與 API inbound。
                  </p>

                  <div
                    v-if="trafficError"
                    class="mt-3 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3.5 py-3 text-[11px] leading-5 text-rose-200"
                    role="alert"
                  >
                    {{ trafficError }}
                  </div>

                  <div class="mt-3 overflow-hidden rounded-xl border border-white/[0.06] bg-white/[0.02]">
                    <div v-if="trafficLoading && traffic.length === 0" class="animate-pulse divide-y divide-white/[0.05]">
                      <div v-for="row in 3" :key="row" class="flex items-center gap-4 px-4 py-4">
                        <div class="h-3 w-40 rounded bg-slate-800/80" />
                        <div class="ml-auto h-3 w-20 rounded bg-slate-800/80" />
                        <div class="h-3 w-20 rounded bg-slate-800/80" />
                      </div>
                    </div>
                    <div v-else-if="traffic.length === 0" class="px-5 py-12 text-center">
                      <div class="mx-auto grid h-10 w-10 place-items-center rounded-full border border-white/[0.06] bg-white/[0.02]">
                        <svg viewBox="0 0 24 24" class="h-4 w-4 text-slate-600" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                          <path d="M4 19V9m5 10V5m5 14v-7m5 7V3" stroke-linecap="round" />
                        </svg>
                      </div>
                      <p class="mt-3 text-xs font-medium text-slate-400">尚無用戶流量</p>
                      <p class="mx-auto mt-1.5 max-w-sm text-[10px] leading-5 text-slate-600">
                        Xray 啟動後的第一筆統計約需 30 秒，且配置必須開啟 Stats API。
                      </p>
                    </div>
                    <div v-else class="overflow-x-auto">
                      <table class="w-full min-w-[620px] border-collapse text-left">
                        <thead>
                          <tr class="border-b border-white/[0.06] text-[9px] uppercase tracking-[0.14em] text-slate-500">
                            <th class="px-4 py-3 font-medium">用戶 / UUID</th>
                            <th class="px-4 py-3 text-right font-medium">上行</th>
                            <th class="px-4 py-3 text-right font-medium">下行</th>
                            <th class="px-4 py-3 text-right font-medium">合計</th>
                            <th class="px-4 py-3 text-right font-medium">配額 / 狀態</th>
                            <th class="px-4 py-3 text-right font-medium">最後更新</th>
                          </tr>
                        </thead>
                        <tbody class="divide-y divide-white/[0.045]">
                          <tr v-for="user in traffic" :key="user.user_email" class="transition-colors hover:bg-white/[0.02]">
                            <td class="px-4 py-3 font-mono text-[11px] text-slate-300">{{ user.user_email }}</td>
                            <td class="px-4 py-3 text-right font-mono text-[11px] tabular-nums text-emerald-300">
                              {{ formatBytes(user.upload_bytes) }}
                            </td>
                            <td class="px-4 py-3 text-right font-mono text-[11px] tabular-nums text-sky-300">
                              {{ formatBytes(user.download_bytes) }}
                            </td>
                            <td class="px-4 py-3 text-right font-mono text-[11px] tabular-nums text-white">
                              {{ formatBytes(user.total_bytes) }}
                            </td>
                            <td class="px-4 py-3 text-right font-mono text-[10px]">
                              <span v-if="user.is_banned" class="rounded border border-rose-400/20 bg-rose-400/[0.07] px-1.5 py-0.5 text-rose-300">
                                已封禁
                              </span>
                              <span v-else-if="user.quota_bytes > 0" class="text-amber-300">
                                {{ formatBytes(user.quota_bytes) }}
                              </span>
                              <span v-else class="text-slate-600">無限</span>
                            </td>
                            <td class="px-4 py-3 text-right font-mono text-[10px] text-slate-500">
                              {{ formatLastSeen(user.last_updated) }}
                            </td>
                          </tr>
                        </tbody>
                      </table>
                    </div>
                  </div>
                </section>

                <section v-show="activeTab === 'cloud'" class="mt-4">
                  <div class="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <p class="label text-emerald-400/80">Cloud Control</p>
                      <h3 class="mt-1 text-sm font-medium text-white">雲端主機控管</h3>
                    </div>
                    <span
                      class="rounded-md border px-2 py-1 font-mono text-[9px] uppercase tracking-[0.12em]"
                      :class="
                        cloudAutoHeal
                          ? 'border-emerald-400/20 bg-emerald-400/[0.07] text-emerald-300'
                          : 'border-slate-500/20 bg-slate-500/[0.06] text-slate-400'
                      "
                    >
                      {{ cloudAutoHeal ? 'Auto-Heal On' : 'Auto-Heal Off' }}
                    </span>
                  </div>

                  <div v-if="superAdmin" class="mt-3 rounded-xl border border-violet-400/15 bg-violet-400/[0.03] p-4">
                    <p class="text-xs font-medium text-violet-200">Provider Credentials</p>
                    <p class="mt-1 text-[10px] text-slate-500">全局 API Key；儲存後 replace-ip / power / bandwidth 會使用真實憑證。</p>
                    <div class="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-[.6fr_1.4fr]">
                      <select v-model="credentialProvider" class="focus-ring rounded-lg border border-white/[0.08] bg-[#0b1119] px-3 py-2 text-xs text-slate-200">
                        <option value="vultr">Vultr</option>
                        <option value="digitalocean">DigitalOcean</option>
                      </select>
                      <input v-model="credentialKey" type="password" autocomplete="new-password" placeholder="API Key / Token" class="focus-ring rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200" />
                    </div>
                    <div class="mt-2 flex flex-wrap items-center gap-2">
                      <button type="button" class="focus-ring rounded-lg border border-white/[0.1] px-3 py-1.5 text-[10px] text-slate-300 disabled:opacity-40" :disabled="credentialTesting" @click="testCredential">{{ credentialTesting ? '測試中…' : '測試連通性' }}</button>
                      <button type="button" class="focus-ring rounded-lg bg-violet-400 px-3 py-1.5 text-[10px] font-semibold text-violet-950 disabled:opacity-40" :disabled="credentialSaving" @click="saveCredential">{{ credentialSaving ? '儲存中…' : '儲存 Key' }}</button>
                      <span v-for="credential in cloudCredentials" :key="credential.provider" class="font-mono text-[9px] text-slate-500">{{ credential.provider }}: {{ credential.masked_key }}</span>
                    </div>
                    <p v-if="credentialError" class="mt-2 text-[10px] text-rose-300">{{ credentialError }}</p>
                    <p v-if="credentialMessage" class="mt-2 text-[10px] text-emerald-300">{{ credentialMessage }}</p>
                  </div>

                  <div v-if="superAdmin" class="mt-3 rounded-xl border border-emerald-400/15 bg-emerald-400/[0.03] p-4">
                    <p class="text-xs font-medium text-emerald-200">TLS / 偽裝站分發</p>
                    <p class="mt-1 text-[10px] text-slate-500">憑證與模板上傳在「憑證」頁；部署會寫入 /etc/nodehub/certs 與 Nginx 443。</p>
                    <div class="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-2">
                      <select v-model.number="selectedCertificateID" class="focus-ring rounded-lg border border-white/[0.08] bg-[#0b1119] px-3 py-2 text-xs text-slate-200">
                        <option :value="null" disabled>選擇證書</option>
                        <option v-for="cert in certificates" :key="cert.id" :value="cert.id">{{ cert.domain }}</option>
                      </select>
                      <select v-model="selectedTemplateID" class="focus-ring rounded-lg border border-white/[0.08] bg-[#0b1119] px-3 py-2 text-xs text-slate-200">
                        <option v-for="template in siteTemplates" :key="template.id" :value="template.id">{{ template.name }}</option>
                      </select>
                    </div>
                    <div class="mt-2 flex flex-wrap items-center gap-2">
                      <button type="button" class="focus-ring rounded-lg bg-emerald-400 px-3 py-1.5 text-[10px] font-semibold text-emerald-950 disabled:opacity-40" :disabled="siteDeploying || !selectedCertificateID" @click="deployFakeSite">{{ siteDeploying ? '部署中…' : '部署偽裝站' }}</button>
                      <router-link to="/certificates" class="text-[10px] text-slate-500 underline hover:text-slate-300">管理證書</router-link>
                    </div>
                    <p v-if="siteError" class="mt-2 text-[10px] text-rose-300">{{ siteError }}</p>
                    <p v-if="siteResult" class="mt-2 font-mono text-[10px] text-emerald-300">{{ siteResult }}</p>
                  </div>

                  <div v-if="superAdmin" class="mt-3 rounded-xl border border-cyan-400/15 bg-cyan-400/[0.03] p-4">
                    <div class="flex flex-wrap items-center justify-between gap-2">
                      <div>
                        <p class="text-xs font-medium text-cyan-200">雲端主機電源控制</p>
                        <p class="mt-1 text-[10px] text-slate-500">即使 SSH / Agent 死亡，也可直接呼叫 Provider API。</p>
                      </div>
                      <button
                        type="button"
                        class="focus-ring rounded-md border border-white/[0.1] px-2.5 py-1 text-[10px] text-slate-300 hover:bg-white/[0.06]"
                        :disabled="cloudBandwidthLoading"
                        @click="loadCloudBandwidth"
                      >
                        {{ cloudBandwidthLoading ? '查詢中…' : '查詢流量' }}
                      </button>
                    </div>
                    <div class="mt-3 flex flex-wrap gap-2">
                      <button
                        v-for="action in (['on', 'off', 'reboot'] as const)"
                        :key="action"
                        type="button"
                        class="focus-ring rounded-lg border border-white/[0.1] px-3 py-2 font-mono text-[10px] uppercase tracking-[0.1em] transition hover:bg-white/[0.06] disabled:opacity-40"
                        :class="action === 'off' ? 'text-rose-300' : action === 'reboot' ? 'text-amber-300' : 'text-emerald-300'"
                        :disabled="!cloudProvider || !cloudInstanceID || cloudActionLoading !== ''"
                        @click="powerCloud(action)"
                      >
                        {{ cloudActionLoading === action ? '…' : `POWER ${action}` }}
                      </button>
                    </div>
                    <p v-if="cloudBandwidth" class="mt-3 font-mono text-[10px] text-cyan-200">
                      BANDWIDTH {{ formatBytes(cloudBandwidth.used_bytes) }} / {{ formatBytes(cloudBandwidth.allocated_bytes) }} · {{ cloudBandwidth.period }}
                    </p>
                    <p v-if="cloudControlError" class="mt-2 text-[10px] text-rose-300">{{ cloudControlError }}</p>
                  </div>

                  <div class="mt-3 rounded-xl border border-white/[0.06] bg-white/[0.02] p-4">
                    <div class="flex flex-wrap items-center justify-between gap-3">
                      <div>
                        <p class="text-xs font-medium text-slate-200">海外 TCP 成功、國內探針連續阻斷後自動換 IP</p>
                        <p class="mt-1 text-[10px] leading-5 text-slate-600">
                          只有 gfw_blocked 會觸發；ADSL 走 pppoe_redial，雲端走 Provider API。預設冷卻 30 分鐘，避免帳單失控。
                        </p>
                      </div>
                      <button
                        type="button"
                        role="switch"
                        :aria-checked="cloudAutoHeal"
                        class="focus-ring relative h-6 w-11 shrink-0 rounded-full border transition"
                        :class="cloudAutoHeal ? 'border-emerald-400/30 bg-emerald-400/30' : 'border-white/[0.1] bg-white/[0.05]'"
                        :disabled="cloudSaving"
                        @click="cloudAutoHeal = !cloudAutoHeal"
                      >
                        <span
                          class="absolute top-0.5 h-4 w-4 rounded-full transition-all"
                          :class="cloudAutoHeal ? 'left-[22px] bg-emerald-300' : 'left-0.5 bg-slate-500'"
                        ></span>
                      </button>
                    </div>

                    <div class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
                      <label class="block">
                        <span class="label">Cloud Provider</span>
                        <select
                          v-model="cloudProvider"
                          class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-[#0b1119] px-3 py-2 text-xs text-slate-200 outline-none focus:border-emerald-400/40"
                          :disabled="cloudSaving"
                        >
                          <option value="">未設定</option>
                          <option value="mock">mock</option>
                          <option value="vultr">vultr</option>
                          <option value="digitalocean">digitalocean</option>
                          <option value="adsl">adsl (PPPoE redial)</option>
                        </select>
                      </label>
                      <label class="block">
                        <span class="label">Provider Instance ID</span>
                        <input
                          v-model="cloudInstanceID"
                          type="text"
                          spellcheck="false"
                          placeholder="instance-xxxxxxxx"
                          class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-emerald-400/40"
                          :disabled="cloudSaving"
                        />
                      </label>
                      <label class="block">
                        <span class="label">DDNS Domain</span>
                        <input
                          v-model="ddnsDomain"
                          type="text"
                          spellcheck="false"
                          placeholder="node1.example.com"
                          class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-emerald-400/40"
                          :disabled="cloudSaving"
                        />
                      </label>
                      <label class="block">
                        <span class="label">DDNS Zone ID</span>
                        <input
                          v-model="ddnsZoneID"
                          type="text"
                          spellcheck="false"
                          placeholder="Cloudflare Zone ID"
                          class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-emerald-400/40"
                          :disabled="cloudSaving"
                        />
                      </label>
                      <label class="block sm:col-span-2">
                        <span class="label">DDNS Record ID（可選）</span>
                        <input
                          v-model="ddnsRecordID"
                          type="text"
                          spellcheck="false"
                          placeholder="留空時以 Domain 查詢 Cloudflare A record"
                          class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-emerald-400/40"
                          :disabled="cloudSaving"
                        />
                      </label>
                    </div>

                    <div
                      v-if="activeNode?.last_auto_heal_at"
                      class="mt-3 font-mono text-[10px] text-slate-600"
                    >
                      LAST AUTO-HEAL: {{ formatLastSeen(activeNode.last_auto_heal_at) }}
                    </div>
                    <div
                      v-if="cloudWarning"
                      class="mt-3 rounded-lg border border-emerald-400/20 bg-emerald-400/[0.06] px-3 py-2 text-[11px] text-emerald-200"
                      role="status"
                    >
                      {{ cloudWarning }}
                    </div>
                    <div
                      v-if="cloudError"
                      class="mt-3 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-[11px] leading-5 text-rose-200"
                      role="alert"
                    >
                      {{ cloudError }}
                    </div>

                    <div class="mt-4 flex flex-wrap items-center justify-between gap-2 border-t border-white/[0.06] pt-4">
                      <p class="text-[10px] text-slate-600">
                        ADSL 會呼叫 Agent 的 pppoe_redial；其他 Provider 會使用雲端 API。請先確認機器可重建。
                      </p>
                      <button
                        v-if="canManage"
                        type="button"
                        class="focus-ring rounded-lg bg-emerald-400 px-3.5 py-2 text-[11px] font-semibold text-emerald-950 transition hover:bg-emerald-300 disabled:cursor-wait disabled:opacity-60"
                        :disabled="cloudSaving"
                        @click="saveCloud"
                      >
                        {{ cloudSaving ? '儲存中…' : '儲存自癒設定' }}
                      </button>
                    </div>
                  </div>

                  <div v-if="superAdmin" class="mt-4 rounded-xl border border-rose-500/15 bg-rose-500/[0.035] p-4">
                    <div class="flex flex-wrap items-center justify-between gap-3">
                      <div>
                        <p class="label text-rose-400/80">Danger Zone</p>
                        <p class="mt-1 text-xs font-medium text-rose-200">手動觸發換 IP</p>
                        <p class="mt-1 text-[10px] leading-5 text-rose-100/50">
                          會呼叫 Cloud Provider；Vultr 流程可能重建機器。成功後自動更新 DDNS。
                          每台節點 24 小時最多 2 次，超過會自動阻斷並發送高危告警。
                        </p>
                      </div>
                      <button
                        type="button"
                        class="focus-ring rounded-lg border border-rose-400/25 bg-rose-400/[0.08] px-3.5 py-2 text-[11px] font-semibold text-rose-200 transition hover:bg-rose-400/[0.14] disabled:cursor-not-allowed disabled:opacity-40"
                        :disabled="!cloudProvider || (cloudProvider !== 'adsl' && !cloudInstanceID.trim()) || replacing"
                        @click="openReplaceConfirm"
                      >
                        {{ replacing ? '換 IP 中…' : '手動換 IP' }}
                      </button>
                    </div>

                    <div
                      v-if="replaceResult"
                      class="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-3"
                    >
                      <div class="rounded-lg border border-white/[0.06] bg-black/20 px-3 py-2">
                        <p class="label">Old IP</p>
                        <p class="mt-1 font-mono text-xs text-slate-400">{{ replaceResult.old_ip || '—' }}</p>
                      </div>
                      <div class="rounded-lg border border-emerald-400/15 bg-emerald-400/[0.04] px-3 py-2">
                        <p class="label text-emerald-400/80">New IP</p>
                        <p class="mt-1 font-mono text-xs text-emerald-300">{{ replaceResult.new_ip }}</p>
                      </div>
                      <div class="rounded-lg border border-white/[0.06] bg-black/20 px-3 py-2">
                        <p class="label">DDNS</p>
                        <p class="mt-1 font-mono text-xs" :class="replaceResult.ddns_updated ? 'text-emerald-300' : 'text-amber-300'">
                          {{ replaceResult.ddns_updated ? 'UPDATED' : 'SKIPPED / FAILED' }}
                        </p>
                      </div>
                    </div>
                    <p
                      v-if="replaceResult?.warning"
                      class="mt-2 font-mono text-[10px] text-amber-300/70"
                    >
                      {{ replaceResult.warning }}
                    </p>
                  </div>

                  <div v-if="superAdmin" class="mt-4 rounded-xl border border-rose-500/25 bg-rose-500/[0.055] p-4">
                    <div class="flex flex-wrap items-center justify-between gap-3">
                      <div>
                        <p class="label text-rose-400/80">Danger Zone</p>
                        <p class="mt-1 text-xs font-medium text-rose-200">移除節點（Decommission）</p>
                        <p class="mt-1 max-w-xl text-[10px] leading-5 text-rose-100/50">
                          中斷目前的 Agent、撤銷 node token，並封鎖此 Node ID 再次以共用 AGENT_TOKEN 回連。這個動作不會刪除遠端機器上的 Agent 服務。
                        </p>
                      </div>
                      <button
                        type="button"
                        class="focus-ring rounded-lg bg-rose-500 px-3.5 py-2 text-[11px] font-semibold text-white transition hover:bg-rose-400"
                        @click="openDeleteConfirm"
                      >
                        移除節點
                      </button>
                    </div>
                  </div>

                  <div v-if="superAdmin" class="mt-4 rounded-xl border border-rose-500/25 bg-rose-500/[0.055] p-4">
                    <div class="flex flex-wrap items-center justify-between gap-3">
                      <div>
                        <p class="label text-rose-400/80">Danger Zone</p>
                        <p class="mt-1 text-xs font-medium text-rose-200">一鍵 DD 系統（破壞性）</p>
                        <p class="mt-1 max-w-xl text-[10px] leading-5 text-rose-100/50">
                          重新安裝 Debian/Ubuntu。Agent 會把重連腳本注入 InstallNET.sh 的 -cmd，首次開機自動下載 Agent 並以 systemd 重連。
                        </p>
                      </div>
                      <button
                        type="button"
                        class="focus-ring rounded-lg bg-rose-500 px-3.5 py-2 text-[11px] font-semibold text-white transition hover:bg-rose-400 disabled:opacity-40"
                        :disabled="!activeNode || activeNode.status !== 'online'"
                        @click="openDDConfirm"
                      >
                        一鍵 DD
                      </button>
                    </div>
                    <p v-if="ddResult" class="mt-3 rounded-lg border border-emerald-400/20 bg-emerald-400/[0.06] px-3 py-2 text-[10px] text-emerald-200">
                      DD 已啟動：{{ ddResult.distro }} {{ ddResult.version }} / {{ ddResult.arch }}
                      <span v-if="ddResult.warning"> · {{ ddResult.warning }}</span>
                    </p>
                  </div>

                  <div
                    v-if="identityOpen"
                    class="fixed inset-0 z-[90] flex items-center justify-center p-4"
                  >
                    <div class="absolute inset-0 bg-black/80 backdrop-blur-sm" @click="identityOpen = false"></div>
                    <div role="dialog" aria-modal="true" class="relative w-full max-w-[460px] rounded-2xl border border-cyan-400/20 bg-[#0b1119] p-5 shadow-panel">
                      <p class="label text-cyan-400/80">Node Identity</p>
                      <h3 class="mt-1 text-lg font-semibold text-white">名稱與對外位址</h3>
                      <p class="mt-2 text-[11px] leading-6 text-slate-400">
                        對外位址會用於節點列表、分享連結與 Xray 訂閱；不會修改 Xray inbound 的 0.0.0.0 監聽設定。
                      </p>
                      <label class="mt-4 block">
                        <span class="label">顯示名稱</span>
                        <input v-model="identityName" class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 text-xs text-slate-100" placeholder="hk01" />
                      </label>
                      <label class="mt-3 block">
                        <span class="label">對外位址 / 域名</span>
                        <input v-model="identityAddress" class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-100" placeholder="hk01.hkvps.xyz" />
                      </label>
                      <div v-if="identityError" class="mt-3 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-[10px] text-rose-200">{{ identityError }}</div>
                      <div class="mt-5 flex justify-end gap-2">
                        <button type="button" class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-[11px] text-slate-400" :disabled="identitySaving" @click="identityOpen = false">取消</button>
                        <button type="button" class="focus-ring rounded-lg bg-cyan-400 px-3 py-2 text-[11px] font-semibold text-cyan-950 disabled:opacity-40" :disabled="identitySaving" @click="saveIdentity">
                          {{ identitySaving ? '儲存中…' : '儲存' }}
                        </button>
                      </div>
                    </div>
                  </div>

                  <div
                    v-if="deleteConfirmOpen"
                    class="fixed inset-0 z-[90] flex items-center justify-center p-4"
                  >
                    <div class="absolute inset-0 bg-black/80 backdrop-blur-sm" @click="deleteConfirmOpen = false"></div>
                    <div role="dialog" aria-modal="true" class="relative w-full max-w-[480px] rounded-2xl border border-rose-500/30 bg-[#0b1119] p-5 shadow-panel">
                      <p class="label text-rose-400/80">Danger Zone / Decommission</p>
                      <h3 class="mt-1 text-lg font-semibold text-white">永久移除 {{ activeNode?.id }}？</h3>
                      <p class="mt-2 text-[11px] leading-6 text-slate-400">
                        會中斷 Agent、撤銷 node token、刪除 Port Forward / Inbound / Xray / 流量紀錄，並封鎖此 Node ID 再次回連。
                        遠端機器上的 systemd 服務不會被自動停止。
                      </p>
                      <label class="mt-4 block">
                        <span class="label">輸入 Node ID 確認</span>
                        <input
                          v-model="deleteConfirmText"
                          :placeholder="activeNode?.id"
                          class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-100"
                          @keyup.enter="confirmDeleteNode"
                        />
                      </label>
                      <div v-if="deleteError" class="mt-3 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-[10px] text-rose-200">{{ deleteError }}</div>
                      <div class="mt-5 flex justify-end gap-2">
                        <button type="button" class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-[11px] text-slate-400" :disabled="deleteLoading" @click="deleteConfirmOpen = false">取消</button>
                        <button type="button" class="focus-ring rounded-lg bg-rose-500 px-3 py-2 text-[11px] font-semibold text-white disabled:opacity-40" :disabled="deleteLoading || deleteConfirmText.trim() !== activeNode?.id" @click="confirmDeleteNode">
                          {{ deleteLoading ? '移除中…' : '確認移除' }}
                        </button>
                      </div>
                    </div>
                  </div>

                  <div
                    v-if="ddConfirmOpen"
                    class="fixed inset-0 z-[85] flex items-center justify-center p-4"
                  >
                    <div class="absolute inset-0 bg-black/80 backdrop-blur-sm" @click="ddConfirmOpen = false"></div>
                    <div role="dialog" aria-modal="true" class="relative w-full max-w-[480px] rounded-2xl border border-rose-500/30 bg-[#0b1119] p-5 shadow-panel">
                      <p class="label text-rose-400/80">Destructive / One-Click DD</p>
                      <h3 class="mt-1 text-lg font-semibold text-white">確認重裝 {{ activeNode?.id }}？</h3>
                      <p class="mt-2 text-[11px] leading-6 text-slate-400">
                        這會清除目標機硬碟並重裝系統。腳本需要能連到公開的 Agent 下載 URL；若重連腳本失敗，機器可能失聯。
                      </p>
                      <div class="mt-4 grid grid-cols-2 gap-2">
                        <label class="block">
                          <span class="label">發行版</span>
                          <select v-model="ddForm.distro" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 text-xs text-slate-200">
                            <option value="debian">Debian</option>
                            <option value="ubuntu">Ubuntu</option>
                          </select>
                        </label>
                        <label class="block">
                          <span class="label">版本</span>
                          <input v-model="ddForm.version" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200" placeholder="12 / 22.04" />
                        </label>
                        <label class="block">
                          <span class="label">架構</span>
                          <select v-model="ddForm.arch" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 text-xs text-slate-200">
                            <option value="amd64">amd64</option>
                            <option value="arm64">arm64</option>
                          </select>
                        </label>
                        <label class="block">
                          <span class="label">Root 密碼（可選）</span>
                          <input v-model="ddForm.rootPassword" type="password" autocomplete="new-password" class="focus-ring mt-1 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-200" />
                        </label>
                      </div>
                      <div v-if="ddError" class="mt-3 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-[10px] text-rose-200">{{ ddError }}</div>
                      <div class="mt-5 flex justify-end gap-2">
                        <button type="button" class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-[11px] text-slate-400" :disabled="ddLoading" @click="ddConfirmOpen = false">取消</button>
                        <button type="button" class="focus-ring rounded-lg bg-rose-500 px-3 py-2 text-[11px] font-semibold text-white disabled:opacity-40" :disabled="ddLoading" @click="submitDD">
                          {{ ddLoading ? '送出中…' : '確認 DD' }}
                        </button>
                      </div>
                    </div>
                  </div>

                  <div
                    v-if="replaceConfirmOpen"
                    class="fixed inset-0 z-[80] flex items-center justify-center p-4"
                  >
                    <div class="absolute inset-0 bg-black/75 backdrop-blur-sm" @click="replaceConfirmOpen = false"></div>
                    <div
                      role="dialog"
                      aria-modal="true"
                      aria-label="確認手動換 IP"
                      class="relative w-full max-w-[430px] rounded-2xl border border-rose-500/20 bg-[#0b1119] p-5 shadow-panel"
                    >
                      <p class="label text-rose-400/80">Dangerous Operation</p>
                      <h3 class="mt-1 text-lg font-semibold text-white">確認更換節點 IP？</h3>
                      <p class="mt-2 text-[11px] leading-6 text-slate-400">
                        這會呼叫 <span class="font-mono text-rose-200">{{ cloudProvider }}</span> 的 ReplaceIP。
                        Vultr 流程可能刪除並重建機器，造成服務中斷。此操作不可直接復原。
                      </p>
                      <div class="mt-4 rounded-lg border border-white/[0.06] bg-black/20 px-3 py-2 font-mono text-[10px] text-slate-500">
                        {{ activeNode?.name }} / {{ activeNode?.id }}<br />
                        Instance: {{ cloudInstanceID || '—' }}
                      </div>
                      <div class="mt-5 flex justify-end gap-2">
                        <button
                          type="button"
                          class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-[11px] text-slate-400 transition hover:bg-white/[0.05]"
                          :disabled="replacing"
                          @click="replaceConfirmOpen = false"
                        >
                          取消
                        </button>
                        <button
                          type="button"
                          class="focus-ring inline-flex items-center gap-2 rounded-lg bg-rose-400 px-3.5 py-2 text-[11px] font-semibold text-rose-950 transition hover:bg-rose-300 disabled:cursor-wait disabled:opacity-60"
                          :disabled="replacing"
                          @click="confirmReplaceIP"
                        >
                          <svg v-if="replacing" viewBox="0 0 24 24" class="h-3.5 w-3.5 animate-spin" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                            <path d="M21 12a9 9 0 1 1-6.2-8.56" stroke-linecap="round" />
                          </svg>
                          {{ replacing ? '執行中…' : '確認更換 IP' }}
                        </button>
                      </div>
                    </div>
                  </div>
                </section>
              </template>
            </div>

            <footer class="flex items-center justify-between border-t border-white/[0.06] px-5 py-3 sm:px-6">
              <p class="font-mono text-[10px] text-slate-600">
                {{ footerSummary }}
              </p>
              <button
                type="button"
                class="focus-ring rounded-lg bg-white/[0.06] px-3.5 py-2 text-xs font-medium text-slate-200 transition hover:bg-white/[0.1] disabled:opacity-40"
                :disabled="saving || xrayBusy || inboundBusy"
                @click="close"
              >
                關閉
              </button>
            </footer>
          </div>
        </div>

        <div v-if="inboundDialogOpen" class="fixed inset-0 z-[70] flex items-center justify-center p-4">
          <div class="absolute inset-0 bg-black/80 backdrop-blur-sm" @click="closeInboundDialog"></div>
          <form
            class="relative flex max-h-[90vh] w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-violet-400/20 bg-[#0a0f18] shadow-panel"
            @submit.prevent="saveInbound"
          >
            <header class="flex items-start justify-between gap-4 border-b border-white/[0.06] px-5 py-4">
              <div>
                <p class="label text-violet-400/80">{{ editingInbound ? 'Edit Inbound' : 'New Inbound' }}</p>
                <h3 class="mt-1 text-base font-semibold text-white">
                  {{ editingInbound ? '編輯入站規則' : '新增入站規則' }}
                </h3>
                <p class="mt-1 text-[10px] text-slate-600">儲存後會重新生成完整 Xray JSON 並下發 Agent。</p>
              </div>
              <button
                type="button"
                class="focus-ring grid h-8 w-8 place-items-center rounded-lg border border-white/[0.07] text-slate-400 transition hover:bg-white/[0.05] disabled:opacity-40"
                aria-label="關閉入站表單"
                :disabled="inboundSaving"
                @click="closeInboundDialog"
              >
                <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                  <path d="m6 6 12 12M18 6 6 18" stroke-linecap="round" />
                </svg>
              </button>
            </header>

            <div class="overflow-y-auto px-5 py-4">
              <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <label class="block sm:col-span-2">
                  <span class="label">備註 Remark</span>
                  <input
                    v-model="inboundForm.remark"
                    type="text"
                    maxlength="128"
                    placeholder="例如：Tokyo Reality 443"
                    class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 text-xs text-slate-100 placeholder:text-slate-600 transition focus:border-violet-400/40"
                    :disabled="inboundSaving"
                  />
                </label>
                <label class="block">
                  <span class="label">端口 Port</span>
                  <input
                    v-model.number="inboundForm.port"
                    type="number"
                    min="1"
                    max="65535"
                    inputmode="numeric"
                    class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-100 transition focus:border-violet-400/40"
                    :disabled="inboundSaving"
                  />
                </label>
                <label class="block">
                  <span class="label">協議 Protocol</span>
                  <select
                    v-model="inboundForm.protocol"
                    class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-[#0b1119] px-3 py-2 text-xs text-slate-200 outline-none focus:border-violet-400/40"
                    :disabled="inboundSaving"
                  >
                    <option value="vless">VLESS</option>
                    <option value="trojan">Trojan</option>
                    <option value="shadowsocks">Shadowsocks</option>
                  </select>
                </label>
              </div>

              <div v-if="isSimple" class="mt-3 rounded-lg border border-emerald-400/15 bg-emerald-400/[0.04] px-3 py-2 text-[10px] leading-5 text-emerald-200/80">
                進階參數已自動化：UUID / 密碼 / 傳輸方式由系統產生，無需手動填寫。
              </div>

              <div v-if="!isSimple" class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                <label class="block">
                  <span class="label">傳輸 Network</span>
                  <select
                    v-model="inboundForm.network"
                    class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-[#0b1119] px-3 py-2 text-xs text-slate-200 outline-none focus:border-violet-400/40"
                    :disabled="inboundSaving || inboundForm.protocol === 'shadowsocks'"
                  >
                    <option v-for="network in availableNetworks" :key="network" :value="network">
                      {{ networkLabel(network) }}
                    </option>
                  </select>
                </label>
                <label class="block">
                  <span class="label">安全 Security</span>
                  <select
                    v-model="inboundForm.security"
                    class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-[#0b1119] px-3 py-2 text-xs text-slate-200 outline-none focus:border-violet-400/40"
                    :disabled="inboundSaving || inboundForm.protocol === 'shadowsocks'"
                  >
                    <option v-for="security in availableSecurities" :key="security" :value="security">
                      {{ securityLabel(security) }}
                    </option>
                  </select>
                </label>
              </div>

              <div v-if="inboundForm.protocol === 'vless' && !isSimple" class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                <label class="block sm:col-span-2">
                  <span class="label">UUID</span>
                  <div class="mt-1.5 flex gap-2">
                    <input
                      v-model="inboundForm.uuid"
                      type="text"
                      spellcheck="false"
                      class="focus-ring min-w-0 flex-1 rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-100 transition focus:border-violet-400/40"
                      :disabled="inboundSaving"
                    />
                    <button
                      type="button"
                      class="focus-ring shrink-0 rounded-lg border border-white/[0.08] px-2.5 py-2 text-[10px] font-medium text-slate-400 transition hover:bg-white/[0.05] hover:text-white"
                      :disabled="inboundSaving"
                      @click="generateInboundSecret"
                    >
                      一鍵 UUID
                    </button>
                  </div>
                </label>
                <label v-if="showFlow" class="block sm:col-span-2">
                  <span class="label">Flow</span>
                  <select
                    v-model="inboundForm.flow"
                    class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-[#0b1119] px-3 py-2 text-xs text-slate-200 outline-none focus:border-violet-400/40"
                    :disabled="inboundSaving"
                  >
                    <option value="">不使用 Flow</option>
                    <option value="xtls-rprx-vision">xtls-rprx-vision</option>
                  </select>
                </label>
              </div>

              <div v-if="inboundForm.protocol === 'trojan' && !isSimple" class="mt-3">
                <label class="block">
                  <span class="label">Trojan 密碼</span>
                  <div class="mt-1.5 flex gap-2">
                    <input
                      v-model="inboundForm.password"
                      type="text"
                      spellcheck="false"
                      class="focus-ring min-w-0 flex-1 rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-100 transition focus:border-violet-400/40"
                      :disabled="inboundSaving"
                    />
                    <button
                      type="button"
                      class="focus-ring shrink-0 rounded-lg border border-white/[0.08] px-2.5 py-2 text-[10px] font-medium text-slate-400 transition hover:bg-white/[0.05] hover:text-white"
                      :disabled="inboundSaving"
                      @click="generateInboundSecret"
                    >
                      一鍵密碼
                    </button>
                  </div>
                </label>
              </div>

              <div v-if="inboundForm.protocol === 'shadowsocks' && !isSimple" class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                <label class="block">
                  <span class="label">加密方法</span>
                  <select
                    v-model="inboundForm.method"
                    class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-[#0b1119] px-3 py-2 font-mono text-xs text-slate-200 outline-none focus:border-violet-400/40"
                    :disabled="inboundSaving"
                  >
                    <option value="2022-blake3-aes-128-gcm">2022-blake3-aes-128-gcm</option>
                    <option value="2022-blake3-aes-256-gcm">2022-blake3-aes-256-gcm</option>
                    <option value="2022-blake3-chacha20-poly1305">2022-blake3-chacha20-poly1305</option>
                    <option value="aes-128-gcm">aes-128-gcm</option>
                    <option value="aes-256-gcm">aes-256-gcm</option>
                    <option value="chacha20-poly1305">chacha20-poly1305</option>
                  </select>
                </label>
                <label class="block">
                  <span class="label">Shadowsocks 密碼 / Key</span>
                  <div class="mt-1.5 flex gap-2">
                    <input
                      v-model="inboundForm.password"
                      type="text"
                      spellcheck="false"
                      class="focus-ring min-w-0 flex-1 rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-100 transition focus:border-violet-400/40"
                      :disabled="inboundSaving"
                    />
                    <button
                      type="button"
                      class="focus-ring shrink-0 rounded-lg border border-white/[0.08] px-2.5 py-2 text-[10px] font-medium text-slate-400 transition hover:bg-white/[0.05] hover:text-white"
                      :disabled="inboundSaving"
                      @click="generateInboundSecret"
                    >
                      一鍵 Key
                    </button>
                  </div>
                </label>
              </div>

              <div v-if="isWebSocket && !isSimple" class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                <label class="block">
                  <span class="label">WebSocket Path</span>
                  <input
                    v-model="inboundForm.path"
                    type="text"
                    placeholder="/edge"
                    class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-100 placeholder:text-slate-600 transition focus:border-violet-400/40"
                    :disabled="inboundSaving"
                  />
                </label>
                <label class="block">
                  <span class="label">Host Header（可選）</span>
                  <input
                    v-model="inboundForm.host"
                    type="text"
                    placeholder="cdn.example.com"
                    class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-100 placeholder:text-slate-600 transition focus:border-violet-400/40"
                    :disabled="inboundSaving"
                  />
                </label>
              </div>

              <label v-if="isGRPC && !isSimple" class="mt-3 block">
                <span class="label">gRPC serviceName</span>
                <input
                  v-model="inboundForm.serviceName"
                  type="text"
                  placeholder="nodehub"
                  class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-xs text-slate-100 placeholder:text-slate-600 transition focus:border-violet-400/40"
                  :disabled="inboundSaving"
                />
              </label>

              <div v-if="isTLS && !isSimple" class="mt-3 rounded-xl border border-emerald-400/15 bg-emerald-400/[0.025] p-3">
                <p class="label text-emerald-400/80">TLS Certificate</p>
                <div class="mt-2 grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <label class="block">
                    <span class="label">Certificate path</span>
                    <input
                      v-model="inboundForm.certificateFile"
                      type="text"
                      placeholder="/etc/nodehub/certs/fullchain.pem"
                      class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-[11px] text-slate-100 placeholder:text-slate-600 focus:border-emerald-400/40"
                      :disabled="inboundSaving"
                    />
                  </label>
                  <label class="block">
                    <span class="label">Key path</span>
                    <input
                      v-model="inboundForm.keyFile"
                      type="text"
                      placeholder="/etc/nodehub/certs/key.pem"
                      class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-[11px] text-slate-100 placeholder:text-slate-600 focus:border-emerald-400/40"
                      :disabled="inboundSaving"
                    />
                  </label>
                  <label class="block sm:col-span-2">
                    <span class="label">serverName / SNI（可選）</span>
                    <input
                      v-model="inboundForm.serverName"
                      type="text"
                      placeholder="edge.example.com"
                      class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-[11px] text-slate-100 placeholder:text-slate-600 focus:border-emerald-400/40"
                      :disabled="inboundSaving"
                    />
                  </label>
                </div>
              </div>

              <div v-if="isReality && !isSimple" class="mt-3 rounded-xl border border-rose-400/15 bg-rose-400/[0.025] p-3">
                <div class="flex flex-wrap items-center justify-between gap-2">
                  <div>
                    <p class="label text-rose-400/80">REALITY</p>
                    <p class="mt-1 text-[10px] text-slate-600">X25519 keypair 由後端安全隨機產生；public key 僅用於分享連結。</p>
                  </div>
                  <button
                    type="button"
                    class="focus-ring inline-flex items-center gap-2 rounded-lg border border-rose-400/20 bg-rose-400/[0.06] px-2.5 py-1.5 text-[10px] font-medium text-rose-200 transition hover:bg-rose-400/[0.12] disabled:opacity-50"
                    :disabled="realityGenerating || inboundSaving"
                    @click="generateRealityPair"
                  >
                    <svg v-if="realityGenerating" viewBox="0 0 24 24" class="h-3 w-3 animate-spin" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                      <path d="M21 12a9 9 0 1 1-6.2-8.56" stroke-linecap="round" />
                    </svg>
                    {{ realityGenerating ? '產生中…' : '產生金鑰' }}
                  </button>
                </div>
                <div class="mt-2 grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <label class="block sm:col-span-2">
                    <span class="label">偽裝 dest（host:port）</span>
                    <input
                      v-model="inboundForm.dest"
                      type="text"
                      placeholder="www.microsoft.com:443"
                      class="focus-ring mt-1.5 w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-[11px] text-slate-100 placeholder:text-slate-600 focus:border-rose-400/40"
                      :disabled="inboundSaving"
                    />
                  </label>
                  <label class="block">
                    <span class="label">Private Key</span>
                    <input
                      v-model="inboundForm.privateKey"
                      type="text"
                      readonly
                      class="mt-1.5 w-full rounded-lg border border-white/[0.06] bg-black/20 px-3 py-2 font-mono text-[10px] text-slate-500"
                    />
                  </label>
                  <label class="block">
                    <span class="label">Public Key</span>
                    <input
                      v-model="inboundForm.publicKey"
                      type="text"
                      readonly
                      class="mt-1.5 w-full rounded-lg border border-white/[0.06] bg-black/20 px-3 py-2 font-mono text-[10px] text-slate-500"
                    />
                  </label>
                  <label class="block sm:col-span-2">
                    <span class="label">Short ID</span>
                    <input
                      v-model="inboundForm.shortID"
                      type="text"
                      readonly
                      class="mt-1.5 w-full rounded-lg border border-white/[0.06] bg-black/20 px-3 py-2 font-mono text-[10px] text-slate-500"
                    />
                  </label>
                </div>
              </div>

              <div
                v-if="inboundFormError"
                class="mt-3 rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-2 text-[11px] leading-5 text-rose-200"
                role="alert"
              >
                {{ inboundFormError }}
              </div>
            </div>

            <footer class="flex items-center justify-between gap-3 border-t border-white/[0.06] px-5 py-3">
              <p class="font-mono text-[9px] text-slate-600">JSON IS GENERATED, NEVER HAND-EDITED</p>
              <div class="flex gap-2">
                <button
                  type="button"
                  class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-[11px] text-slate-400 transition hover:bg-white/[0.05]"
                  :disabled="inboundSaving"
                  @click="closeInboundDialog"
                >
                  取消
                </button>
                <button
                  type="submit"
                  class="focus-ring inline-flex items-center gap-2 rounded-lg bg-violet-400 px-3.5 py-2 text-[11px] font-semibold text-violet-950 transition hover:bg-violet-300 disabled:cursor-wait disabled:opacity-60"
                  :disabled="inboundSaving"
                >
                  <svg v-if="inboundSaving" viewBox="0 0 24 24" class="h-3.5 w-3.5 animate-spin" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                    <path d="M21 12a9 9 0 1 1-6.2-8.56" stroke-linecap="round" />
                  </svg>
                  {{ inboundSaving ? '生成／下發中…' : editingInbound ? '儲存並重新生成' : '建立並下發' }}
                </button>
              </div>
            </footer>
          </form>
        </div>

        <div v-if="shareOpen" class="fixed inset-0 z-[75] flex items-center justify-center p-4">
          <div class="absolute inset-0 bg-black/80 backdrop-blur-sm" @click="closeShare"></div>
          <div class="relative w-full max-w-md overflow-hidden rounded-2xl border border-emerald-400/20 bg-[#0a0f18] shadow-panel">
            <header class="flex items-start justify-between gap-3 border-b border-white/[0.06] px-5 py-4">
              <div>
                <p class="label text-emerald-400/80">Share / QR</p>
                <h3 class="mt-1 text-base font-semibold text-white">{{ shareInbound?.remark || '分享連結' }}</h3>
                <p class="mt-1 text-[10px] text-slate-600">v2rayN / Shadowrocket / Streisand 可直接匯入</p>
              </div>
              <button
                type="button"
                class="focus-ring grid h-8 w-8 place-items-center rounded-lg border border-white/[0.07] text-slate-400 transition hover:bg-white/[0.05]"
                aria-label="關閉分享視窗"
                @click="closeShare"
              >
                <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                  <path d="m6 6 12 12M18 6 6 18" stroke-linecap="round" />
                </svg>
              </button>
            </header>
            <div class="px-5 py-5">
              <div v-if="shareLoading" class="flex flex-col items-center justify-center py-10">
                <svg viewBox="0 0 24 24" class="h-6 w-6 animate-spin text-emerald-300" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                  <path d="M21 12a9 9 0 1 1-6.2-8.56" stroke-linecap="round" />
                </svg>
                <p class="mt-3 text-xs text-slate-500">生成分享連結…</p>
              </div>
              <div v-else-if="shareError" class="rounded-lg border border-rose-500/20 bg-rose-500/[0.06] px-3 py-3 text-[11px] text-rose-200">
                {{ shareError }}
              </div>
              <template v-else>
                <div v-if="shareQR" class="mx-auto w-fit rounded-xl bg-white p-3">
                  <img :src="shareQR" alt="代理分享二維碼" class="h-56 w-56" />
                </div>
                <textarea
                  :value="shareLink"
                  readonly
                  rows="4"
                  spellcheck="false"
                  class="mt-4 w-full resize-none rounded-lg border border-white/[0.08] bg-black/30 p-3 font-mono text-[10px] leading-5 text-slate-300"
                ></textarea>
                <div class="mt-3 flex gap-2">
                  <button
                    type="button"
                    class="focus-ring inline-flex flex-1 items-center justify-center gap-2 rounded-lg bg-emerald-400 px-3 py-2 text-[11px] font-semibold text-emerald-950 transition hover:bg-emerald-300"
                    @click="copyShareLink"
                  >
                    <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                      <path d="M9 9h11v11H9zM4 15V4h11" stroke-linecap="round" stroke-linejoin="round" />
                    </svg>
                    {{ shareCopied ? '已複製' : '複製連結' }}
                  </button>
                  <button
                    type="button"
                    class="focus-ring rounded-lg border border-white/[0.08] px-3 py-2 text-[11px] text-slate-400 transition hover:bg-white/[0.05]"
                    @click="closeShare"
                  >
                    關閉
                  </button>
                </div>
                <p class="mt-3 break-all font-mono text-[9px] text-slate-600">
                  {{ shareInbound?.security === 'reality' ? 'REALITY · 需要客戶端支援 XTLS Vision' : networkLabel(shareInbound?.network || 'tcp') }}
                </p>
              </template>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
