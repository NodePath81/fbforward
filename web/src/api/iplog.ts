import { callRPC } from './rpc';
import type {
  GeoIPStatusResponse,
  LogEventQueryParams,
  LogEventQueryResult,
  IPLogQueryParams,
  IPLogQueryResult,
  IPLogStatusResponse,
  RejectionLogQueryParams,
  RejectionLogQueryResult,
  RefreshGeoIPResponse,
  RPCResponse
} from '../types';

export async function getGeoIPStatus(
  token: string
): Promise<RPCResponse<GeoIPStatusResponse>> {
  return callRPC<GeoIPStatusResponse>(token, 'GetGeoIPStatus', {});
}

export async function refreshGeoIP(
  token: string
): Promise<RPCResponse<RefreshGeoIPResponse>> {
  return callRPC<RefreshGeoIPResponse>(token, 'RefreshGeoIP', {});
}

export async function getIPLogStatus(
  token: string
): Promise<RPCResponse<IPLogStatusResponse>> {
  return callRPC<IPLogStatusResponse>(token, 'GetIPLogStatus', {});
}

export async function queryIPLog(
  token: string,
  params: IPLogQueryParams
): Promise<RPCResponse<IPLogQueryResult>> {
  return callRPC<IPLogQueryResult>(token, 'QueryIPLog', params);
}

export async function queryRejectionLog(
  token: string,
  params: RejectionLogQueryParams
): Promise<RPCResponse<RejectionLogQueryResult>> {
  return callRPC<RejectionLogQueryResult>(token, 'QueryRejectionLog', params);
}

export async function queryLogEvents(
  token: string,
  params: LogEventQueryParams
): Promise<RPCResponse<LogEventQueryResult>> {
  return callRPC<LogEventQueryResult>(token, 'QueryLogEvents', params);
}
