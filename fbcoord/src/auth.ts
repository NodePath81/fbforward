export function extractBearerToken(request: Request): string | null {
  const header = request.headers.get('authorization') ?? '';
  const prefix = 'Bearer ';
  if (!header.startsWith(prefix)) {
    return null;
  }
  const token = header.slice(prefix.length).trim();
  return token === '' ? null : token;
}

export function extractSourceIp(request: Request): string {
  return request.headers.get('cf-connecting-ip')?.trim() || 'unknown';
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
