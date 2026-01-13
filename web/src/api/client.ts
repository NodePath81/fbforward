export async function apiFetch(path: string, token: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers || {});
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }
  return fetch(path, {
    ...init,
    headers
  });
}

export async function fetchText(path: string, token: string, init: RequestInit = {}): Promise<string> {
  const res = await apiFetch(path, token, init);
  return res.text();
}

export async function fetchJSON<T>(path: string, token: string, init: RequestInit = {}): Promise<T> {
  const res = await apiFetch(path, token, init);
  return res.json() as Promise<T>;
}
