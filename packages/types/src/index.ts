/**
 * @multica/types - Shared type definitions
 * Zero dependencies, foundation for all other packages
 */

// ============================================================================
// Base Message Types
// ============================================================================

export interface Message {
  id: string
  payload: unknown
  timestamp: number
}

// ============================================================================
// Connection State
// ============================================================================

export type ConnectionState =
  | 'disconnected'
  | 'connecting'
  | 'connected'
  | 'reconnecting'

// ============================================================================
// Agent Types
// ============================================================================

export type AgentStatus = 'idle' | 'running' | 'closed'

export interface AgentInfo {
  agentId: string
  status: AgentStatus
}

// ============================================================================
// Provider Types
// ============================================================================

export type AuthMethod = 'api-key' | 'oauth'

export interface ProviderMeta {
  id: string
  name: string
  authMethod: AuthMethod
  defaultModel: string
  models: string[]
  loginUrl?: string
  loginCommand?: string
}

// ============================================================================
// Tool Types
// ============================================================================

export interface ToolInfo {
  name: string
  group: string
  enabled: boolean
}

// ============================================================================
// Skill Types
// ============================================================================

export type SkillSource = 'bundled' | 'global' | 'profile'

export interface SkillInfo {
  id: string
  name: string
  description: string
  version: string
  enabled: boolean
  source: SkillSource
  triggers: string[]
}

// ============================================================================
// Device Types
// ============================================================================

export interface DeviceInfo {
  deviceId: string
  userAgent?: string
  platform?: string
  language?: string
  createdAt: number
  lastSeenAt: number
}

// ============================================================================
// Hub Types
// ============================================================================

export interface HubStatus {
  hubId: string
  status: string
  agentCount: number
  gatewayConnected: boolean
  gatewayUrl?: string
  defaultAgent?: AgentInfo | null
}

// ============================================================================
// Channel Types
// ============================================================================

export type ChannelType = 'telegram' | 'discord' | 'slack'

export interface ChannelAccountState {
  channelId: string
  accountId: string
  status: 'running' | 'stopped' | 'error'
  error?: string
}

// ============================================================================
// Cron Types
// ============================================================================

export type CronJobStatus = 'pending' | 'running' | 'completed' | 'failed'

export interface CronJobInfo {
  id: string
  name: string
  description: string
  enabled: boolean
  schedule: string
  nextRunAt: string | null
  lastStatus: CronJobStatus | null
  lastRunAt: string | null
}

// ============================================================================
// RPC Types
// ============================================================================

export interface RpcRequest {
  method: string
  params?: unknown
  id?: string
}

export interface RpcResponse<T = unknown> {
  result?: T
  error?: {
    code: number
    message: string
    data?: unknown
  }
  id?: string
}

// ============================================================================
// Event Types
// ============================================================================

export type StreamEventType =
  | 'message_start'
  | 'message_update'
  | 'message_end'
  | 'tool_execution_start'
  | 'tool_execution_update'
  | 'tool_execution_end'
  | 'compaction_start'
  | 'compaction_end'
  | 'agent_error'

export interface StreamEvent {
  type: StreamEventType
  agentId: string
  streamId?: string
  data?: unknown
}

// ============================================================================
// Approval Types
// ============================================================================

export type RiskLevel = 'safe' | 'needs-review' | 'dangerous'

export type ApprovalDecision = 'allow-once' | 'allow-always' | 'deny'

export interface ExecApprovalRequest {
  approvalId: string
  agentId: string
  command: string
  cwd?: string
  riskLevel: RiskLevel
  riskReasons: string[]
  expiresAtMs: number
}

// ============================================================================
// Auth Types
// ============================================================================

export interface AuthUser {
  uid: string
  name: string
  email?: string
  icon?: string
  vip?: number
}
