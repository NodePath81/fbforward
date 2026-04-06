const POOL_NAME_PATTERN = /^[a-z0-9][a-z0-9_-]{0,63}$/;
const MAX_NODE_ID_LENGTH = 128;
const MAX_UPSTREAM_LENGTH = 128;
export const MAX_UPSTREAMS = 20;

export function validatePoolName(pool: string): string | null {
  const value = pool.trim();
  if (!value) {
    return 'missing pool';
  }
  if (!POOL_NAME_PATTERN.test(value)) {
    return 'invalid pool name';
  }
  return null;
}

export function validateNodeId(nodeId: string): string | null {
  const value = nodeId.trim();
  if (!value) {
    return 'invalid node_id';
  }
  if (value.length > MAX_NODE_ID_LENGTH) {
    return `node_id must be at most ${MAX_NODE_ID_LENGTH} characters`;
  }
  return null;
}

export function validateUpstreamTag(tag: string): string | null {
  const value = tag.trim();
  if (!value) {
    return 'upstream tags must not be empty';
  }
  if (value.length > MAX_UPSTREAM_LENGTH) {
    return `upstream tags must be at most ${MAX_UPSTREAM_LENGTH} characters`;
  }
  return null;
}
