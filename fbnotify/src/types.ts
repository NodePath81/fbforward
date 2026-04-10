export type Severity = 'info' | 'warn' | 'critical';
export type ProviderTargetType = 'webhook' | 'pushover' | 'capture';

export interface NotificationSource {
  service: string;
  instance: string;
}

export interface NotificationEvent {
  schema_version: number;
  event_name: string;
  severity: Severity;
  timestamp: string | number;
  source: NotificationSource;
  attributes: Record<string, unknown>;
}

export interface OperatorTokenInfo {
  masked_prefix: string;
  created_at: number;
}

export interface NodeTokenInfo {
  key_id: string;
  source_service: string;
  source_instance: string;
  masked_prefix: string;
  created_at: number;
  last_used_at: number | null;
}

export interface CreateNodeTokenResponse {
  key_id: string;
  token: string;
  info: NodeTokenInfo;
}

export interface WebhookTargetConfig {
  type: 'webhook';
  url: string;
}

export interface PushoverTargetConfig {
  type: 'pushover';
  api_token: string;
  user_key: string;
  device?: string;
}

export interface CaptureTargetConfig {
  type: 'capture';
}

export type ProviderTargetConfig = WebhookTargetConfig | PushoverTargetConfig | CaptureTargetConfig;

export interface ProviderTargetRecord {
  id: string;
  name: string;
  type: ProviderTargetType;
  config: ProviderTargetConfig;
  created_at: number;
  updated_at: number;
}

export interface ProviderTargetSummary {
  id: string;
  name: string;
  type: ProviderTargetType;
  created_at: number;
  updated_at: number;
  summary: Record<string, string | number | boolean | null>;
}

export interface RouteRecord {
  id: string;
  name: string;
  source_service: string | null;
  event_name: string | null;
  target_ids: string[];
  created_at: number;
  updated_at: number;
}

export interface RouteSummary extends RouteRecord {
  match_kind: 'global_default' | 'service_default' | 'event' | 'service_event';
}

export interface DeliveryResult {
  ok: boolean;
  status: number | null;
  target_id: string;
  target_name: string;
  target_type: ProviderTargetType;
  error?: string;
}

export interface CaptureMessage {
  id: string;
  target_id: string;
  target_name: string;
  target_type: ProviderTargetType;
  event_name: string;
  severity: Severity;
  source_service: string;
  source_instance: string;
  received_at: number;
  payload: string;
}
