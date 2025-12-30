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

export function formatTime(ms: number): string {
  if (!Number.isFinite(ms)) {
    return '-';
  }
  return new Date(ms).toLocaleTimeString();
}
