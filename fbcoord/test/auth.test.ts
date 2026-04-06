import { describe, expect, it } from 'vitest';

import { extractClientKey, normalizeClientKey } from '../src/auth';

describe('auth helpers', () => {
  it('buckets ipv6 addresses by /64', () => {
    expect(normalizeClientKey('2001:0db8:abcd:0012:1111:2222:3333:4444'))
      .toBe('2001:0db8:abcd:0012::/64');
    expect(normalizeClientKey('2001:db8:abcd:12::1'))
      .toBe('2001:0db8:abcd:0012::/64');
  });

  it('uses a deterministic dev-prefixed fallback without cf-connecting-ip', () => {
    const request = new Request('https://example.com/api/auth/login', {
      headers: {
        'user-agent': 'vitest-agent'
      }
    });

    expect(extractClientKey(request)).toBe('dev:vitest-agent');
  });
});
