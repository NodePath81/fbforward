export function isAuthorized(request: Request, expectedToken: string): boolean {
  const header = request.headers.get('authorization') ?? '';
  const prefix = 'Bearer ';
  if (!header.startsWith(prefix)) {
    return false;
  }
  return header.slice(prefix.length).trim() === expectedToken;
}
