import type { ConnectionEntry } from '../../types';
import { clearChildren, createEl } from '../../utils/dom';
import { formatAge, formatBytes, formatTime } from '../../utils/format';

export function createConnectionTable(tbody: HTMLElement) {
  return (entries: ConnectionEntry[]) => {
    clearChildren(tbody);
    if (entries.length === 0) {
      const row = createEl('tr');
      const cell = createEl('td', 'empty-row', 'No active entries');
      cell.setAttribute('colspan', '7');
      row.appendChild(cell);
      tbody.appendChild(row);
      return;
    }
    for (const entry of entries) {
      const row = createEl('tr');
      row.appendChild(createCell(entry.id));
      row.appendChild(createCell(entry.clientAddr));
      row.appendChild(createCell(entry.upstream));
      row.appendChild(createCell(formatBytes(entry.bytesUp)));
      row.appendChild(createCell(formatBytes(entry.bytesDown)));
      row.appendChild(createCell(formatTime(entry.lastActivity)));
      row.appendChild(createCell(formatAge(entry.age)));
      tbody.appendChild(row);
    }
  };
}

function createCell(value: string) {
  const cell = createEl('td');
  cell.textContent = value;
  return cell;
}
