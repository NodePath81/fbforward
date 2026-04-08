import { callRPC } from './rpc';
import type {
  GeoIPStatusResponse,
  IPLogQueryParams,
  IPLogQueryResult,
  IPLogStatusResponse,
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
