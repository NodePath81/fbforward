import { describe, expect, it } from 'vitest';

import { selectSharedUpstream } from '../src/coordination/selector';

describe('selectSharedUpstream', () => {
  it('selects the shared upstream with the lowest summed rank', () => {
    const upstream = selectSharedUpstream([
      { nodeId: 'n1', upstreams: ['b', 'a', 'c'] },
      { nodeId: 'n2', upstreams: ['a', 'b', 'c'] },
      { nodeId: 'n3', upstreams: ['b', 'c', 'a'] }
    ]);

    expect(upstream).toBe('b');
  });

  it('returns null when there is no shared upstream', () => {
    const upstream = selectSharedUpstream([
      { nodeId: 'n1', upstreams: ['a'] },
      { nodeId: 'n2', upstreams: ['b'] }
    ]);

    expect(upstream).toBeNull();
  });

  it('returns null when any active node submits an empty list', () => {
    const upstream = selectSharedUpstream([
      { nodeId: 'n1', upstreams: ['a', 'b'] },
      { nodeId: 'n2', upstreams: [] }
    ]);

    expect(upstream).toBeNull();
  });
});
