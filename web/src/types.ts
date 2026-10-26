export type NodeStatus = 'online' | 'offline' | 'provisioning' | 'offline_network_error' | 'gfw_blocked'

export interface AgentNode {
  id: string
  name: string
  address: string
  remote_ip?: string
  status: NodeStatus
  connected_at?: string
  last_seen?: string
  network_error_at?: string
  gfw_blocked_at?: string
  online_seconds: number
  agent_version: string
  os: string
  arch: string
  plugins: string[]
  tags: string[]
  cloud_provider?: string
  provider_instance_id?: string
  ddns_domain?: string
  ddns_zone_id?: string
  ddns_record_id?: string
  auto_heal: boolean
  name_manual?: boolean
  last_auto_heal_at?: string
  cpu_percent: number
  memory_percent: number
  cpu_history: number[]
  created_at: string
}

export interface NodeListResponse {
  nodes: AgentNode[]
  server_time: string
  total: number
  page: number
  limit: number
  total_pages: number
}

export interface NodeListParams {
  page?: number
  limit?: number
  search?: string
  status?: string
  tag?: string
  signal?: AbortSignal
}

export interface DashboardStats {
  nodes_total: number
  nodes_online: number
  nodes_offline: number
  nodes_gfw_blocked: number
  nodes_provisioning: number
  monthly_upload_bytes: number
  monthly_download_bytes: number
  banned_users: number
  average_cpu_percent: number
  average_memory_percent: number
  telemetry_nodes: number
}

export interface NodeDetailResponse {
  node: AgentNode
  server_time: string
}

export interface SystemInfo {
  hostname: string
  os: string
  platform?: string
  arch: string
  cpu_model?: string
  cpu_cores: number
  cpu_percent: number
  memory_total_bytes: number
  memory_used_bytes: number
  memory_percent: number
  uptime_seconds: number
  collected_at: string
  warnings?: string[]
}

export interface CommandResponse {
  request_id: string
  node_id: string
  plugin: string
  duration_ms: number
  data: unknown
}

export interface DeployRequest {
  host: string
  port: number
  password?: string
  private_key?: string
  passphrase?: string
}

export interface DeployResponse {
  status: string
  node_id: string
  message: string
  elapsed_ms: number
}

export interface PortForwardRule {
  rule_id: string
  node_id: string
  listen_port: number
  protocol: 'tcp' | 'udp'
  target_host: string
  target_port: number
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface PortForwardListResponse {
  rules: PortForwardRule[]
}

export interface CreatePortForwardRequest {
  listen_port: number
  protocol: 'tcp' | 'udp'
  target_host: string
  target_port: number
  enabled?: boolean
}

export interface UpdatePortForwardRequest {
  listen_port?: number
  protocol?: 'tcp' | 'udp'
  target_host?: string
  target_port?: number
  enabled?: boolean
}

export interface PortForwardMutationResponse {
  rule?: PortForwardRule
  deleted?: boolean
  synced: boolean
  warning?: string
}

export interface TelemetrySample {
  node_id: string
  timestamp: string
  cpu_percent: number
  memory_percent: number
  memory_used_bytes: number
  memory_total_bytes: number
  net_rx_bytes: number
  net_tx_bytes: number
}

export interface TelemetrySnapshot {
  samples: TelemetrySample[]
}

export interface TelemetryView extends TelemetrySample {
  receivedAt: number
  rxBytesPerSecond: number
  txBytesPerSecond: number
}

export type InboundProtocol = 'vless' | 'trojan' | 'shadowsocks'
export type InboundNetwork = 'tcp' | 'ws' | 'grpc'
export type InboundSecurity = 'none' | 'tls' | 'reality'

export interface ProxyInboundSettings {
  uuid?: string
  flow?: string
  password?: string
  method?: string
  path?: string
  host?: string
  service_name?: string
  private_key?: string
  public_key?: string
  short_id?: string
  dest?: string
  server_names?: string[]
  certificate_file?: string
  key_file?: string
  server_name?: string
}

export interface ProxyInbound {
  id: number
  node_id: string
  remark: string
  port: number
  protocol: InboundProtocol
  network: InboundNetwork
  security: InboundSecurity
  settings: ProxyInboundSettings
  created_at: string
  updated_at: string
}

export interface ProxyInboundListResponse {
  inbounds: ProxyInbound[]
  node_host: string
}

export interface ProxyInboundMutationResponse {
  inbound: ProxyInbound
  synced: boolean
  warning?: string
}

export interface ProxyInboundRequest {
  remark: string
  port: number
  protocol: InboundProtocol
  network: InboundNetwork
  security: InboundSecurity
  settings: ProxyInboundSettings
}

export interface RealityKeysResponse {
  private_key: string
  public_key: string
  short_id: string
}

export interface InboundShareResponse {
  link: string
  host: string
  port: number
  protocol: string
  remark: string
}

export interface XrayConfigResponse {
  node_id: string
  config: Record<string, unknown>
  updated_at: string
}

export interface XrayConfigMutationResponse {
  config?: Record<string, unknown>
  updated_at?: string
  deleted?: boolean
  synced: boolean
  warning?: string
}

export interface UserTraffic {
  node_id: string
  user_email: string
  upload_bytes: number
  download_bytes: number
  total_bytes: number
  quota_bytes: number
  is_banned: boolean
  last_updated: string
}

export interface UserTrafficResponse {
  users: UserTraffic[]
}

export interface TrafficDaily {
  Day: string
  UploadBytes: number
  DownloadBytes: number
}

export interface TrafficHistoryResponse {
  days: number
  history: TrafficDaily[] | null
}

export interface TrafficUserSummary {
  user_email: string
  upload_bytes: number
  download_bytes: number
  total_bytes: number
  node_count: number
  last_updated: string
  quota_bytes: number
  device_limit: number
  is_banned: boolean
}

export interface TrafficUserSummaryResponse {
  users: TrafficUserSummary[]
}

export interface TraefikServer {
  url: string
  status?: string
}

export interface TraefikRoute {
  name: string
  rule: string
  entry_points: string[]
  service: string
  middlewares: string[]
  tls: boolean
  status: string
  servers: TraefikServer[]
}

export interface TraefikRoutesResponse {
  configured?: boolean
  demo?: boolean
  routes: TraefikRoute[]
  overview?: Record<string, unknown>
}

export interface TraefikLogsResponse {
  configured?: boolean
  demo?: boolean
  lines: string[]
  source?: string
}

export interface TraefikSettings {
  api_url: string
  username: string
  log_path: string
  password_configured: boolean
  updated_at?: string
}

export interface NotificationSettings {
  telegram_configured: boolean
  telegram_chat_id: string
  webhook_configured: boolean
  updated_at?: string
}

export interface TOTPSetupResponse {
  secret: string
  otpauth_url: string
  enabled: boolean
  period_seconds: number
}

export interface AdminAPIKey {
  id: number
  name: string
  prefix: string
  revoked: boolean
  last_used_at?: string
  created_at: string
}

export interface AdminAPIKeyListResponse {
  api_keys: AdminAPIKey[]
}

export interface SpeedtestResult {
  ping_ms: number
  download_mbps: number
  upload_mbps: number
  download_bytes: number
  upload_bytes: number
  server: string
  timestamp: string
  duration_ms: number
}

export interface SpeedtestResponse {
  result: SpeedtestResult
  elapsed_ms: number
}

export interface TagCount {
  tag: string
  count: number
}

export interface TagListResponse {
  tags: TagCount[]
}

export interface BatchCommandRequest {
  tags: string[]
  node_ids?: string[]
  all_online?: boolean
  plugin: string
  payload: Record<string, unknown>
  timeout_ms?: number
}

export interface BatchNodeResult {
  node_id: string
  name: string
  success: boolean
  error?: string
  duration_ms: number
}

export interface BatchCommandResponse {
  matched: number
  success: BatchNodeResult[]
  failed: BatchNodeResult[]
  all_online?: boolean
  plugin: string
  tags: string[]
  total_ms: number
}

export interface CloudSettingsRequest {
  cloud_provider: string
  provider_instance_id: string
  ddns_domain: string
  ddns_zone_id: string
  ddns_record_id: string
  auto_heal: boolean
}

export interface CloudSettingsResponse {
  node: AgentNode
}

export interface ReplaceIPResponse {
  node_id: string
  provider: string
  old_ip: string
  new_ip: string
  new_instance_id: string
  ddns_updated: boolean
  ddns_domain: string
  warning: string
}

export interface AuditLog {
  id: number
  timestamp: string
  username: string
  action: string
  target_node_id: string
  details: string
  ip_address: string
}

export interface AuditLogResponse {
  logs: AuditLog[]
  total: number
  page: number
  page_size: number
  total_pages: number
}

export interface LoginResponse {
  token: string
  expires_at: string
  user: {
    id: number
    username: string
    role: AdminRole
    allowed_tags: string[]
  }
}

export type AdminRole = 'superadmin' | 'operator' | 'viewer'

export interface AdminUser {
  id: number
  username: string
  role: AdminRole
  allowed_tags: string[]
  created_at: string
}

export interface AdminUserListResponse {
  users: AdminUser[]
}

export interface AdminUserRequest {
  username?: string
  password?: string
  role: AdminRole
  allowed_tags: string[]
}

export interface CloudPowerResponse {
  node_id: string
  provider: string
  action: string
}

export interface CloudBandwidth {
  used_bytes: number
  allocated_bytes: number
  period: string
}

export interface CloudBandwidthResponse {
  node_id: string
  provider: string
  bandwidth: CloudBandwidth
}

export interface DDRequest {
  distro: 'debian' | 'ubuntu'
  version: string
  arch: 'amd64' | 'arm64'
  root_password?: string
}

export interface CertificateView {
  id: number
  domain: string
  created_at: string
  updated_at: string
}

export interface CertificateListResponse {
  certificates: CertificateView[]
}

export interface SiteTemplate {
  id: string
  name: string
  description: string
}

export interface SiteTemplateListResponse {
  templates: SiteTemplate[]
}

export interface SiteDeployResponse {
  deployed: boolean
  node_id: string
  domain: string
  template: string
  cert_dir: string
  web_root: string
}

export interface DeleteNodeResponse {
  deleted: boolean
  node_id: string
}

export interface DDResponse {
  accepted: boolean
  node_id: string
  distro: string
  version: string
  arch: string
  warning?: string
}
