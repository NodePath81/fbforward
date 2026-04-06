export function extractBearerToken(request: Request): string | null {
  const header = request.headers.get('authorization') ?? '';
  const prefix = 'Bearer ';
  if (!header.startsWith(prefix)) {
    return null;
  }
  const token = header.slice(prefix.length).trim();
  return token === '' ? null : token;
}

function parseIPv4(value: string): string | null {
  const parts = value.trim().split('.');
  if (parts.length !== 4) {
    return null;
  }
  const normalized: string[] = [];
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part)) {
      return null;
    }
    const numeric = Number(part);
    if (!Number.isInteger(numeric) || numeric < 0 || numeric > 255) {
      return null;
    }
    normalized.push(String(numeric));
  }
  return normalized.join('.');
}

function expandIPv6(value: string): string[] | null {
  const trimmed = value.trim().toLowerCase().split('%', 1)[0] ?? '';
  if (!trimmed || !trimmed.includes(':')) {
    return null;
  }

  const [head, tail, extra] = trimmed.split('::');
  if (extra !== undefined) {
    return null;
  }

  const parseSide = (segment: string): string[] | null => {
    if (segment === '') {
      return [];
    }
    const parts = segment.split(':');
    const parsed: string[] = [];
    for (const part of parts) {
      if (part === '') {
        return null;
      }
      if (part.includes('.')) {
        const ipv4 = parseIPv4(part);
        if (!ipv4) {
          return null;
        }
        const octets = ipv4.split('.').map(Number);
        parsed.push(
          (((octets[0] ?? 0) << 8) | (octets[1] ?? 0)).toString(16),
          (((octets[2] ?? 0) << 8) | (octets[3] ?? 0)).toString(16)
        );
        continue;
      }
      if (!/^[0-9a-f]{1,4}$/.test(part)) {
        return null;
      }
      parsed.push(part);
    }
    return parsed;
  };

  const left = parseSide(head ?? '');
  const right = parseSide(tail ?? '');
  if (!left || !right) {
    return null;
  }

  const missing = 8 - (left.length + right.length);
  if (tail === undefined) {
    if (missing !== 0) {
      return null;
    }
    return [...left];
  }
  if (missing < 1) {
    return null;
  }
  return [
    ...left,
    ...Array.from({ length: missing }, () => '0'),
    ...right
  ];
}

export function normalizeClientKey(value: string): string {
  const ipv4 = parseIPv4(value);
  if (ipv4) {
    return ipv4;
  }

  const ipv6 = expandIPv6(value);
  if (ipv6) {
    return `${ipv6.slice(0, 4).map(part => part.padStart(4, '0')).join(':')}::/64`;
  }

  return value.trim().toLowerCase().replace(/\s+/g, '-').slice(0, 128) || 'anonymous';
}

export function extractClientKey(request: Request): string {
  const cloudflareIp = request.headers.get('cf-connecting-ip')?.trim();
  if (cloudflareIp) {
    return normalizeClientKey(cloudflareIp);
  }

  const fallbackSource = [
    request.headers.get('x-real-ip')?.trim(),
    request.headers.get('x-forwarded-for')?.split(',')[0]?.trim(),
    request.headers.get('x-client-ip')?.trim(),
    request.headers.get('user-agent')?.trim()
  ].find((candidate): candidate is string => Boolean(candidate && candidate.length > 0));

  return `dev:${normalizeClientKey(fallbackSource ?? 'anonymous')}`;
}

export function isAllowedOrigin(request: Request): boolean {
  const origin = request.headers.get('origin');
  if (!origin) {
    return true;
  }

  try {
    return new URL(origin).origin === new URL(request.url).origin;
  } catch {
    return false;
  }
}

export function getCookie(request: Request, name: string): string | null {
  const header = request.headers.get('cookie');
  if (!header) {
    return null;
  }

  for (const part of header.split(';')) {
    const [rawName, ...rest] = part.split('=');
    if (rawName?.trim() !== name) {
      continue;
    }
    return decodeURIComponent(rest.join('=').trim());
  }

  return null;
}
