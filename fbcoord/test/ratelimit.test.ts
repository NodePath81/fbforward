import { describe, expect, it } from 'vitest';

import { RateLimiter } from '../src/ratelimit';

describe('RateLimiter', () => {
  it('blocks after the configured number of failures', () => {
    let now = 0;
    const limiter = new RateLimiter(3, 10_000, 15_000, () => now);

    limiter.recordFailure('1.1.1.1');
    limiter.recordFailure('1.1.1.1');
    expect(limiter.getStatus('1.1.1.1').blocked).toBe(false);

    limiter.recordFailure('1.1.1.1');
    expect(limiter.getStatus('1.1.1.1').blocked).toBe(true);
  });

  it('resets failures after a successful authentication', () => {
    const limiter = new RateLimiter();

    limiter.recordFailure('1.1.1.1');
    limiter.recordFailure('1.1.1.1');
    limiter.recordSuccess('1.1.1.1');

    expect(limiter.getStatus('1.1.1.1')).toEqual({ blocked: false, retryAfterSeconds: 0 });
  });

  it('expires blocks after the cooldown period', () => {
    let now = 0;
    const limiter = new RateLimiter(3, 10_000, 15_000, () => now);

    limiter.recordFailure('1.1.1.1');
    limiter.recordFailure('1.1.1.1');
    limiter.recordFailure('1.1.1.1');

    now = 16_000;
    expect(limiter.getStatus('1.1.1.1').blocked).toBe(false);
  });

  it('tracks IPs independently', () => {
    const limiter = new RateLimiter();

    limiter.recordFailure('1.1.1.1');
    limiter.recordFailure('1.1.1.1');
    limiter.recordFailure('2.2.2.2');

    expect(limiter.getStatus('1.1.1.1').blocked).toBe(false);
    expect(limiter.getStatus('2.2.2.2').blocked).toBe(false);
  });
});
