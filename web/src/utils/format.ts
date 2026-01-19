export function formatBytes(value: number): string {
  if (!Number.isFinite(value)) {
    return '-';
  }
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let idx = 0;
  let val = value;
  while (val >= 1024 && idx < units.length - 1) {
    val /= 1024;
    idx++;
  }
  return `${val.toFixed(1)} ${units[idx]}`;
}

export function formatBps(value: number): string {
  if (!Number.isFinite(value)) {
    return '-';
  }
  const units = ['bps', 'Kbps', 'Mbps', 'Gbps', 'Tbps'];
  let idx = 0;
  let val = value;
  while (val >= 1000 && idx < units.length - 1) {
    val /= 1000;
    idx++;
  }
  return `${val.toFixed(1)} ${units[idx]}`;
}

export function formatPercent(value: number, digits = 1): string {
  if (!Number.isFinite(value)) {
    return '-';
  }
  return `${(value * 100).toFixed(digits)}%`;
}

export function formatMs(value: number, digits = 2): string {
  if (!Number.isFinite(value)) {
    return '-';
  }
  return `${value.toFixed(digits)} ms`;
}

export function formatScore(value: number): string {
  if (!Number.isFinite(value)) {
    return '-';
  }
  return value.toFixed(1);
}

export function formatAge(seconds: number): string {
  if (!Number.isFinite(seconds)) {
    return '-';
  }
  return `${Math.round(seconds)}s`;
}

export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) {
    return '-';
  }
  const total = Math.floor(seconds);
  const days = Math.floor(total / 86400);
  const hours = Math.floor((total % 86400) / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const secs = total % 60;

  const parts: string[] = [];
  if (days > 0) {
    parts.push(`${days}d`);
  }
  if (hours > 0 || parts.length > 0) {
    parts.push(`${hours}h`);
  }
  if (minutes > 0 || parts.length > 0) {
    parts.push(`${minutes}m`);
  }
  if (parts.length === 0) {
    parts.push(`${secs}s`);
  }
  return parts.join(' ');
}

export function formatScheduledTime(isoString: string): string {
  const scheduled = new Date(isoString);
  if (Number.isNaN(scheduled.getTime())) {
    return '-';
  }
  const now = new Date();
  const diffMs = scheduled.getTime() - now.getTime();

  if (diffMs < 0) {
    return 'due now';
  }

  const diffSec = Math.floor(diffMs / 1000);
  if (diffSec < 60) {
    return `in ${diffSec}s`;
  }

  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) {
    return `in ${diffMin}m`;
  }

  const diffHour = Math.floor(diffMin / 60);
  if (diffHour < 24) {
    return `in ${diffHour}h`;
  }

  const diffDay = Math.floor(diffHour / 24);
  return `in ${diffDay}d`;
}

export function formatTime(ms: number): string {
  if (!Number.isFinite(ms)) {
    return '-';
  }
  return new Date(ms).toLocaleTimeString();
}
