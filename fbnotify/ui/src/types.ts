export type Severity = 'info' | 'warn' | 'critical';
export type ProviderTargetType = 'webhook' | 'pushover' | 'capture';

export interface NotificationEvent {
  schema_version: number;
  event_name: string;
  severity: Severity;
  timestamp: string | number;
  source: {
    service: string;
    instance: string;
  };
  attributes: Record<string, unknown>;
}

export interface OperatorTokenInfo {
  masked_prefix: string;
  created_at: number;
}

export interface TokenRotateResponse extends OperatorTokenInfo {
  token?: string;
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

export interface ProviderTargetSummary {
  id: string;
  name: string;
  type: ProviderTargetType;
  created_at: number;
  updated_at: number;
  summary: Record<string, string | number | boolean | null>;
}

export interface RouteSummary {
  id: string;
  name: string;
  source_service: string | null;
  event_name: string | null;
  target_ids: string[];
  created_at: number;
  updated_at: number;
  match_kind: 'global_default' | 'service_default' | 'event' | 'service_event';
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

export interface TestSendResponse {
  target_count: number;
  results: Array<{
    ok: boolean;
    status: number | null;
    target_id: string;
    target_name: string;
    target_type: ProviderTargetType;
    error?: string;
  }>;
}
