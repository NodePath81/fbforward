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
