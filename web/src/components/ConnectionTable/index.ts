import type { ConnectionEntry } from '../../types';
import { clearChildren, createEl } from '../../utils/dom';
import { formatAge, formatBytes, formatBytesRate, formatTime } from '../../utils/format';

export function createConnectionTable(tbody: HTMLElement) {
  return (entries: ConnectionEntry[]) => {
    clearChildren(tbody);
    if (entries.length === 0) {
      const row = createEl('tr');
      const cell = createEl('td', 'empty-row', 'No active entries');
      cell.setAttribute('colspan', '12');
      row.appendChild(cell);
      tbody.appendChild(row);
      return;
    }
    for (const entry of entries) {
      const age = Math.max(0, (Date.now() - entry.createdAt) / 1000);
      const row = createEl('tr');
      row.appendChild(createCell(entry.kind.toUpperCase(), 'protocol-cell'));
      row.appendChild(createCell(entry.clientAddr));
      row.appendChild(createCell(String(entry.port)));
      row.appendChild(createCell(entry.upstream));
      row.appendChild(createCell(formatBytes(entry.bytesUp)));
      row.appendChild(createCell(formatBytes(entry.bytesDown)));
      row.appendChild(createCell(formatBytesRate(entry.rateUp)));
      row.appendChild(createCell(formatBytesRate(entry.rateDown)));
      row.appendChild(createCell(formatCount(entry.segmentsUp)));
      row.appendChild(createCell(formatCount(entry.segmentsDown)));
      row.appendChild(createCell(formatTime(entry.lastActivity)));
      row.appendChild(createCell(formatAge(age)));
      tbody.appendChild(row);
    }
  };
}

function formatCount(value: number): string {
  if (!Number.isFinite(value)) {
    return '-';
  }
  return value.toLocaleString();
}

function createCell(value: string, className?: string) {
  const cell = createEl('td', className);
  cell.textContent = value;
  return cell;
}
