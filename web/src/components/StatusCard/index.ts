import { createEl, clearChildren } from '../../utils/dom';
import { formatBytes } from '../../utils/format';
import type {
  CoordinationStatus,
  GeoIPDBStatus,
  GeoIPStatusResponse,
  IPLogStatusResponse,
  Mode
} from '../../types';

interface StatusCardOptions {
  onRefreshGeoIP?: () => void;
}

interface StatusData {
  mode: Mode;
  activeUpstream: string;
  coordination: CoordinationStatus;
  tcp: number;
  udp: number;
  memoryBytes: number;
  goroutines: number;
  geoipStatus: GeoIPStatusResponse | null;
  ipLogStatus: IPLogStatusResponse | null;
  geoipError: string | null;
  ipLogError: string | null;
  refreshGeoIPInFlight: boolean;
}

export function renderStatusCard(
  container: HTMLElement,
  options: StatusCardOptions = {}
): (data: StatusData) => void {
  clearChildren(container);
  const title = createEl('div', 'status-title', 'Live status');
  const rows = {
    mode: createRow('Mode'),
    active: createRow('Active upstream'),
    coordination: createRow('Coordination'),
    coordPick: createRow('Coord pick'),
    coordFallback: createRow('Coord fallback'),
    tcp: createRow('TCP conns'),
    udp: createRow('UDP mappings'),
    memory: createRow('Memory (alloc)'),
    goroutines: createRow('Goroutines'),
    ipLog: createRow('IP log DB'),
    geoipASN: createRow('GeoIP ASN'),
    geoipCountry: createRow('GeoIP Country')
  };
  const divider = createEl('div', 'status-divider');
  const actions = createEl('div', 'status-actions');
  const refreshButton = createEl('button', 'secondary status-action-button', 'Refresh GeoIP') as HTMLButtonElement;
  refreshButton.type = 'button';
  refreshButton.addEventListener('click', () => {
    options.onRefreshGeoIP?.();
  });
  actions.appendChild(refreshButton);

  container.appendChild(title);
  container.appendChild(rows.mode.row);
  container.appendChild(rows.active.row);
  container.appendChild(rows.coordination.row);
  container.appendChild(rows.coordPick.row);
  container.appendChild(rows.coordFallback.row);
  container.appendChild(rows.tcp.row);
  container.appendChild(rows.udp.row);
  container.appendChild(rows.memory.row);
  container.appendChild(rows.goroutines.row);
  container.appendChild(divider);
  container.appendChild(rows.ipLog.row);
  container.appendChild(rows.geoipASN.row);
  container.appendChild(rows.geoipCountry.row);
  container.appendChild(actions);

  return (data: StatusData) => {
    rows.mode.value.textContent = data.mode;
    rows.active.value.textContent = data.activeUpstream || '-';
    rows.coordination.value.textContent = formatCoordinationState(data.coordination);
    rows.coordPick.value.textContent = formatCoordinationPick(data.coordination);
    rows.coordFallback.value.textContent = data.coordination.available
      ? data.coordination.fallback_active
        ? 'local auto'
        : 'inactive'
      : '-';
    rows.tcp.value.textContent = data.tcp.toString();
    rows.udp.value.textContent = data.udp.toString();
    rows.memory.value.textContent = formatBytes(data.memoryBytes);
    rows.goroutines.value.textContent = Number.isFinite(data.goroutines)
      ? Math.round(data.goroutines).toString()
      : '-';
    rows.ipLog.value.textContent = formatIPLogStatus(data.ipLogStatus, data.ipLogError);
    rows.geoipASN.value.textContent = formatGeoIPStatus(data.geoipStatus?.asn_db || null, data.geoipError);
    rows.geoipCountry.value.textContent = formatGeoIPStatus(
      data.geoipStatus?.country_db || null,
      data.geoipError
    );
    refreshButton.disabled = data.refreshGeoIPInFlight;
    refreshButton.textContent = data.refreshGeoIPInFlight ? 'Refreshing...' : 'Refresh GeoIP';
  };
}

function formatCoordinationState(coordination: CoordinationStatus): string {
  if (!coordination.available) {
    return 'not configured';
  }
  const endpoint = [coordination.pool, coordination.node_id].filter(Boolean).join(' / ');
  if (!endpoint) {
    return coordination.connected ? 'connected' : 'disconnected';
  }
  return `${coordination.connected ? 'connected' : 'disconnected'} (${endpoint})`;
}

function formatCoordinationPick(coordination: CoordinationStatus): string {
  if (!coordination.available) {
    return '-';
  }
  if (coordination.selected_upstream) {
    return coordination.selected_upstream;
  }
  return 'waiting for match';
}

function createRow(label: string) {
  const row = createEl('div', 'status-row');
  const name = createEl('span');
  name.textContent = label;
  const value = createEl('strong');
  value.textContent = '-';
  row.appendChild(name);
  row.appendChild(value);
  return { row, value };
}

function formatGeoIPStatus(status: GeoIPDBStatus | null, error: string | null): string {
  if (error) {
    return 'unavailable';
  }
  if (!status) {
    return '-';
  }
  if (!status.configured) {
    return 'not configured';
  }
  if (!status.available) {
    return 'inactive';
  }
  if (status.file_size > 0) {
    return `available · ${formatBytes(status.file_size)}`;
  }
  return 'available';
}

function formatIPLogStatus(status: IPLogStatusResponse | null, error: string | null): string {
  if (error) {
    return 'unavailable';
  }
  if (!status) {
    return '-';
  }
  return `${status.total_record_count.toLocaleString()} total / ${status.rejection_record_count.toLocaleString()} rejects · ${formatBytes(status.file_size)}`;
}
