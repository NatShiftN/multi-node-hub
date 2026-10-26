import { API_BASE } from './config'
import { ApiError, http } from './http'
import type {
  AuditLogResponse,
  BatchCommandRequest,
  BatchCommandResponse,
  AdminAPIKeyListResponse,
  AdminUserListResponse,
  AdminUserRequest,
  NotificationSettings,
  TOTPSetupResponse,
  TraefikLogsResponse,
  TraefikRoutesResponse,
  TraefikSettings,
  TrafficHistoryResponse,
  TrafficUserSummaryResponse,
  CertificateListResponse,
  CertificateView,
  CloudSettingsRequest,
  CloudSettingsResponse,
  CloudBandwidthResponse,
  CloudPowerResponse,
  DDRequest,
  DDResponse,
  DeleteNodeResponse,
  DashboardStats,
  NodeListParams,
  CommandResponse,
  CreatePortForwardRequest,
  DeployRequest,
  DeployResponse,
  NodeDetailResponse,
  NodeListResponse,
  PortForwardListResponse,
  InboundShareResponse,
  PortForwardMutationResponse,
  ProxyInboundListResponse,
  ProxyInboundMutationResponse,
  ProxyInboundRequest,
  RealityKeysResponse,
  ReplaceIPResponse,
  SiteDeployResponse,
  SiteTemplateListResponse,
  SpeedtestResponse,
  TagListResponse,
  UpdatePortForwardRequest,
  UserTrafficResponse,
  XrayConfigMutationResponse,
  XrayConfigResponse,
} from './types'

export { ApiError }

export async function changePassword(
  currentPassword: string,
  newPassword: string,
): Promise<void> {
  await http.post('/api/admin/password', {
    current_password: currentPassword,
    new_password: newPassword,
  })
}

export async function listNodes(params: NodeListParams = {}): Promise<NodeListResponse> {
  const response = await http.get<NodeListResponse>('/api/nodes', {
    signal: params.signal,
    params: {
      page: params.page ?? 1,
      limit: params.limit ?? 20,
      search: params.search || undefined,
      status: params.status || undefined,
      tag: params.tag || undefined,
    },
    headers: { 'Cache-Control': 'no-store' },
  })
  return response.data
}

export async function getDashboardStats(): Promise<DashboardStats> {
  const response = await http.get<DashboardStats>('/api/dashboard/stats', {
    headers: { 'Cache-Control': 'no-store' },
  })
  return response.data
}

export async function listAdminUsers(): Promise<AdminUserListResponse> {
  const response = await http.get<AdminUserListResponse>('/api/admin/users')
  return response.data
}

export async function createAdminUser(payload: AdminUserRequest): Promise<void> {
  await http.post('/api/admin/users', payload)
}

export async function updateAdminUser(id: number, payload: AdminUserRequest): Promise<void> {
  await http.patch(`/api/admin/users/${id}`, payload)
}

export async function deleteAdminUser(id: number): Promise<void> {
  await http.delete(`/api/admin/users/${id}`)
}

export async function cloudPower(
  nodeId: string,
  action: 'off' | 'on' | 'reboot',
): Promise<CloudPowerResponse> {
  const response = await http.post<CloudPowerResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/cloud/power`,
    { action },
    { timeout: 90_000 },
  )
  return response.data
}

export async function getCloudBandwidth(nodeId: string): Promise<CloudBandwidthResponse> {
  const response = await http.get<CloudBandwidthResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/cloud/bandwidth`,
  )
  return response.data
}

export async function ddNode(nodeId: string, payload: DDRequest): Promise<DDResponse> {
  const response = await http.post<DDResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/dd`,
    payload,
    { timeout: 120_000 },
  )
  return response.data
}

export async function getDemoStatus(): Promise<{ demo: boolean }> {
  const response = await http.get<{ demo: boolean }>('/api/demo')
  return response.data
}

export async function getTrafficHistory(days = 30): Promise<TrafficHistoryResponse> {
  const response = await http.get<TrafficHistoryResponse>(`/api/traffic/history?days=${days}`)
  return response.data
}

export async function listTrafficUsers(): Promise<TrafficUserSummaryResponse> {
  const response = await http.get<TrafficUserSummaryResponse>('/api/traffic/users')
  return response.data
}

export async function setTrafficUserQuota(email: string, quotaBytes: number): Promise<void> {
  await http.put(`/api/traffic/users/${encodeURIComponent(email)}/quota`, {
    quota_bytes: quotaBytes,
  })
}

export async function banTrafficUser(email: string): Promise<void> {
  await http.post(`/api/traffic/users/${encodeURIComponent(email)}/ban`)
}

export async function unbanTrafficUser(email: string): Promise<void> {
  await http.post(`/api/traffic/users/${encodeURIComponent(email)}/unban`)
}

export async function getTraefikSettings(): Promise<TraefikSettings> {
  const response = await http.get<TraefikSettings>('/api/traefik/settings')
  return response.data
}

export async function saveTraefikSettings(payload: {
  api_url: string
  username: string
  password: string
  log_path: string
}): Promise<void> {
  await http.put('/api/traefik/settings', payload)
}

export async function testTraefik(): Promise<void> {
  await http.post('/api/traefik/test')
}

export async function getTraefikRoutes(): Promise<TraefikRoutesResponse> {
  const response = await http.get<TraefikRoutesResponse>('/api/traefik/routes')
  return response.data
}

export async function getTraefikLogs(lines = 200): Promise<TraefikLogsResponse> {
  const response = await http.get<TraefikLogsResponse>(`/api/traefik/logs?lines=${lines}`)
  return response.data
}

export async function getNotificationSettings(): Promise<NotificationSettings> {
  const response = await http.get<NotificationSettings>('/api/notifications/settings')
  return response.data
}

export async function saveNotificationSettings(payload: {
  telegram_bot_token: string
  telegram_chat_id: string
  webhook_url: string
}): Promise<void> {
  await http.put('/api/notifications/settings', payload)
}

export async function testNotification(): Promise<void> {
  await http.post('/api/notifications/test')
}

export async function getTOTPStatus(): Promise<{ enabled: boolean }> {
  const response = await http.get<{ enabled: boolean }>('/api/admin/2fa')
  return response.data
}

export async function setupTOTP(): Promise<TOTPSetupResponse> {
  const response = await http.post<TOTPSetupResponse>('/api/admin/2fa/setup')
  return response.data
}

export async function enableTOTP(code: string): Promise<void> {
  await http.post('/api/admin/2fa/enable', { code })
}

export async function disableTOTP(password: string): Promise<void> {
  await http.post('/api/admin/2fa/disable', { password })
}

export async function downloadBackup(): Promise<Blob> {
  const response = await http.get('/api/system/backup', { responseType: 'blob' })
  return response.data as Blob
}

export async function listAPIKeys(): Promise<AdminAPIKeyListResponse> {
  const response = await http.get<AdminAPIKeyListResponse>('/api/api-keys')
  return response.data
}

export async function createAPIKey(name: string): Promise<{ api_key: { id: number; prefix: string }; token: string }> {
  const response = await http.post('/api/api-keys', { name })
  return response.data
}

export async function revokeAPIKey(id: number): Promise<void> {
  await http.delete(`/api/api-keys/${id}`)
}

export async function getXrayStatus(nodeId: string): Promise<{ status: unknown; duration_ms: number }> {
  const response = await http.get(`/api/nodes/${encodeURIComponent(nodeId)}/xray/status`)
  return response.data
}

export async function restartXray(nodeId: string): Promise<void> {
  await http.post(`/api/nodes/${encodeURIComponent(nodeId)}/xray/restart`)
}

export async function listCertificates(): Promise<CertificateListResponse> {
  const response = await http.get<CertificateListResponse>('/api/certificates')
  return response.data
}

export async function createCertificate(payload: {
  domain: string
  cert_pem: string
  key_pem: string
}): Promise<CertificateView> {
  const response = await http.post<CertificateView>('/api/certificates', payload)
  return response.data
}

export async function deleteCertificate(id: number): Promise<void> {
  await http.delete(`/api/certificates/${id}`)
}

export async function listSiteTemplates(): Promise<SiteTemplateListResponse> {
  const response = await http.get<SiteTemplateListResponse>('/api/site-templates')
  return response.data
}

export async function deploySite(
  nodeId: string,
  certificateId: number,
  templateId: string,
): Promise<SiteDeployResponse> {
  const response = await http.post<SiteDeployResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/site`,
    { certificate_id: certificateId, template_id: templateId },
    { timeout: 150_000 },
  )
  return response.data
}

export async function listCloudCredentials(): Promise<{
  credentials: { provider: string; masked_key: string; updated_at: string }[]
}> {
  const response = await http.get('/api/cloud/credentials')
  return response.data
}

export async function saveCloudCredential(provider: string, apiKey: string): Promise<void> {
  await http.put('/api/cloud/credentials', { provider, api_key: apiKey })
}

export async function testCloudCredential(provider: string, apiKey: string): Promise<void> {
  await http.post('/api/cloud/credentials/test', { provider, api_key: apiKey })
}

export async function updateNodeIdentity(
  nodeId: string,
  name: string,
  address: string,
): Promise<NodeDetailResponse> {
  const response = await http.patch<NodeDetailResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/identity`,
    { name, address },
  )
  return response.data
}

export async function deleteNode(nodeId: string): Promise<DeleteNodeResponse> {
  const response = await http.delete<DeleteNodeResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}`,
  )
  return response.data
}

export async function getNode(nodeId: string): Promise<NodeDetailResponse> {
  const response = await http.get<NodeDetailResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}`,
    { headers: { 'Cache-Control': 'no-store' } },
  )
  return response.data
}

export async function executeCommand(
  nodeId: string,
  plugin: string,
  payload: Record<string, unknown> = {},
): Promise<CommandResponse> {
  const response = await http.post<CommandResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/command`,
    { plugin, payload },
  )
  return response.data
}

export async function deployNode(payload: DeployRequest): Promise<DeployResponse> {
  // The password exists only in this request body; it is never stored by the
  // UI or included in URLs.
  const response = await http.post<DeployResponse>('/api/deploy', payload, {
    timeout: 120_000,
  })
  return response.data
}

export async function listPortForwards(
  nodeId: string,
): Promise<PortForwardListResponse> {
  const response = await http.get<PortForwardListResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/port-forwards`,
    { headers: { 'Cache-Control': 'no-store' } },
  )
  return response.data
}

export async function createPortForward(
  nodeId: string,
  payload: CreatePortForwardRequest,
): Promise<PortForwardMutationResponse> {
  const response = await http.post<PortForwardMutationResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/port-forwards`,
    payload,
  )
  return response.data
}

export async function updatePortForward(
  nodeId: string,
  ruleId: string,
  payload: UpdatePortForwardRequest,
): Promise<PortForwardMutationResponse> {
  const response = await http.patch<PortForwardMutationResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/port-forwards/${encodeURIComponent(ruleId)}`,
    payload,
  )
  return response.data
}

export async function deletePortForward(
  nodeId: string,
  ruleId: string,
): Promise<PortForwardMutationResponse> {
  const response = await http.delete<PortForwardMutationResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/port-forwards/${encodeURIComponent(ruleId)}`,
  )
  return response.data
}

export async function getXrayConfig(nodeId: string): Promise<XrayConfigResponse | null> {
  try {
    const response = await http.get<XrayConfigResponse>(
      `/api/nodes/${encodeURIComponent(nodeId)}/xray`,
      { headers: { 'Cache-Control': 'no-store' } },
    )
    return response.data
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return null
    throw error
  }
}

export async function listProxyInbounds(nodeId: string): Promise<ProxyInboundListResponse> {
  const response = await http.get<ProxyInboundListResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/inbounds`,
    { headers: { 'Cache-Control': 'no-store' } },
  )
  return response.data
}

export async function createProxyInbound(
  nodeId: string,
  payload: ProxyInboundRequest,
): Promise<ProxyInboundMutationResponse> {
  const response = await http.post<ProxyInboundMutationResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/inbounds`,
    payload,
    { timeout: 190_000 },
  )
  return response.data
}

export async function updateProxyInbound(
  nodeId: string,
  inboundId: number,
  payload: ProxyInboundRequest,
): Promise<ProxyInboundMutationResponse> {
  const response = await http.patch<ProxyInboundMutationResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/inbounds/${inboundId}`,
    payload,
    { timeout: 190_000 },
  )
  return response.data
}

export async function deleteProxyInbound(
  nodeId: string,
  inboundId: number,
): Promise<ProxyInboundMutationResponse> {
  const response = await http.delete<ProxyInboundMutationResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/inbounds/${inboundId}`,
    { timeout: 60_000 },
  )
  return response.data
}

export async function generateRealityKeys(nodeId: string): Promise<RealityKeysResponse> {
  const response = await http.post<RealityKeysResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/inbounds/reality-keys`,
    {},
    { timeout: 30_000 },
  )
  return response.data
}

export async function getInboundShare(
  nodeId: string,
  inboundId: number,
): Promise<InboundShareResponse> {
  const response = await http.get<InboundShareResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/inbounds/${inboundId}/share`,
    { headers: { 'Cache-Control': 'no-store' }, timeout: 30_000 },
  )
  return response.data
}

export async function saveXrayConfig(
  nodeId: string,
  config: Record<string, unknown>,
): Promise<XrayConfigMutationResponse> {
  const response = await http.put<XrayConfigMutationResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/xray`,
    { config },
    { timeout: 190_000 },
  )
  return response.data
}

export async function deleteXrayConfig(
  nodeId: string,
): Promise<XrayConfigMutationResponse> {
  const response = await http.delete<XrayConfigMutationResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/xray`,
    { timeout: 60_000 },
  )
  return response.data
}

export async function listAuditLogs(page = 1, pageSize = 50): Promise<AuditLogResponse> {
  const response = await http.get<AuditLogResponse>('/api/audit-logs', {
    params: { page, page_size: pageSize },
    headers: { 'Cache-Control': 'no-store' },
  })
  return response.data
}

export async function updateNodeCloud(
  nodeId: string,
  settings: CloudSettingsRequest,
): Promise<CloudSettingsResponse> {
  const response = await http.patch<CloudSettingsResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/cloud`,
    settings,
  )
  return response.data
}

export async function replaceNodeIP(nodeId: string): Promise<ReplaceIPResponse> {
  const response = await http.post<ReplaceIPResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/replace-ip`,
    {},
    { timeout: 330_000 },
  )
  return response.data
}

export async function setNodeTags(nodeId: string, tags: string[]): Promise<void> {
  await http.put(`/api/nodes/${encodeURIComponent(nodeId)}/tags`, { tags })
}

export async function listTags(): Promise<TagListResponse> {
  const response = await http.get<TagListResponse>('/api/tags', {
    headers: { 'Cache-Control': 'no-store' },
  })
  return response.data
}

export async function runBatchCommand(
  request: BatchCommandRequest,
): Promise<BatchCommandResponse> {
  const response = await http.post<BatchCommandResponse>('/api/batch/command', request, {
    timeout: 220_000,
  })
  return response.data
}

export async function runSpeedtest(nodeId: string): Promise<SpeedtestResponse> {
  const response = await http.post<SpeedtestResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/speedtest`,
    {},
    { timeout: 120_000 },
  )
  return response.data
}

export async function listUserTraffic(nodeId: string): Promise<UserTrafficResponse> {
  const response = await http.get<UserTrafficResponse>(
    `/api/nodes/${encodeURIComponent(nodeId)}/traffic`,
    { headers: { 'Cache-Control': 'no-store' } },
  )
  return response.data
}

export function telemetryEventsURL(): string {
  return `${API_BASE}/api/events`
}

export function apiBase(): string {
  return API_BASE || 'same-origin (Vite proxy)'
}
