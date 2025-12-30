import { createEl, clearChildren } from '../../utils/dom';
import type { Mode } from '../../types';

interface StatusData {
  mode: Mode;
  activeUpstream: string;
  tcp: number;
  udp: number;
}

export function renderStatusCard(container: HTMLElement): (data: StatusData) => void {
  clearChildren(container);
  const title = createEl('div', 'status-title', 'Live status');
  const rows = {
    mode: createRow('Mode'),
    active: createRow('Active upstream'),
    tcp: createRow('TCP conns'),
    udp: createRow('UDP mappings')
  };

  container.appendChild(title);
  container.appendChild(rows.mode.row);
  container.appendChild(rows.active.row);
  container.appendChild(rows.tcp.row);
  container.appendChild(rows.udp.row);

  return (data: StatusData) => {
    rows.mode.value.textContent = data.mode;
    rows.active.value.textContent = data.activeUpstream || '-';
    rows.tcp.value.textContent = data.tcp.toString();
    rows.udp.value.textContent = data.udp.toString();
  };
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
