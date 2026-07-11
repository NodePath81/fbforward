import { callRPC } from './rpc';
import type { FirewallStatusResponse, OnlineRule, RPCResponse } from '../types';

export function getFirewallStatus(token: string): Promise<RPCResponse<FirewallStatusResponse>> {
  return callRPC<FirewallStatusResponse>(token, 'GetFirewallStatus', {});
}

export function listOnlineRules(
  token: string,
  includeExpired = true
): Promise<RPCResponse<OnlineRule[]>> {
  return callRPC<OnlineRule[]>(token, 'ListOnlineRules', { include_expired: includeExpired });
}

export function deleteOnlineRule(token: string, ruleId: string): Promise<RPCResponse<unknown>> {
  return callRPC<unknown>(token, 'DeleteOnlineRule', { rule_id: ruleId });
}

export function expireOnlineRule(token: string, ruleId: string): Promise<RPCResponse<unknown>> {
  return callRPC<unknown>(token, 'ExpireOnlineRule', { rule_id: ruleId });
}
