export async function apiFetch(
  path: string,
  token: string,
  init: RequestInit = {},
  timeoutMs: number = 10000
): Promise<Response> {
  const headers = new Headers(init.headers || {});
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }

  const controller = new AbortController();
  const timeoutId = window.setTimeout(() => controller.abort(), timeoutMs);

  try {
    const response = await fetch(path, {
      ...init,
      headers,
      signal: controller.signal
    });
    window.clearTimeout(timeoutId);
    return response;
  } catch (err) {
    window.clearTimeout(timeoutId);
    if (err instanceof Error && err.name === 'AbortError') {
      throw new Error(`Request timeout after ${timeoutMs}ms`);
    }
    throw err;
  }
}

export async function fetchText(
  path: string,
  token: string,
  init: RequestInit = {},
  timeoutMs?: number
): Promise<string> {
  const res = await apiFetch(path, token, init, timeoutMs);
  return res.text();
}

export async function fetchJSON<T>(
  path: string,
  token: string,
  init: RequestInit = {},
  timeoutMs?: number
): Promise<T> {
  const res = await apiFetch(path, token, init, timeoutMs);
  return res.json() as Promise<T>;
}
