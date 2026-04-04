export interface RateLimitStatus {
  blocked: boolean;
  retryAfterSeconds: number;
}

interface IPRecord {
  failures: number;
  firstFailureAt: number;
  blockedUntil: number;
}

export class RateLimiter {
  private readonly records = new Map<string, IPRecord>();

  constructor(
    private readonly maxFailures: number = 3,
    private readonly windowMs: number = 10 * 60_000,
    private readonly blockMs: number = 15 * 60_000,
    private readonly now: () => number = () => Date.now()
  ) {}

  getStatus(ip: string): RateLimitStatus {
    const record = this.records.get(ip);
    if (!record) {
      return { blocked: false, retryAfterSeconds: 0 };
    }

    const now = this.now();
    if (record.blockedUntil > now) {
      return {
        blocked: true,
        retryAfterSeconds: Math.max(1, Math.ceil((record.blockedUntil - now) / 1000))
      };
    }

    if (now-record.firstFailureAt > this.windowMs) {
      this.records.delete(ip);
      return { blocked: false, retryAfterSeconds: 0 };
    }

    return { blocked: false, retryAfterSeconds: 0 };
  }

  recordFailure(ip: string): void {
    const now = this.now();
    const current = this.records.get(ip);
    if (!current || current.blockedUntil <= now && now-current.firstFailureAt > this.windowMs) {
      this.records.set(ip, {
        failures: 1,
        firstFailureAt: now,
        blockedUntil: 0
      });
      return;
    }

    if (current.blockedUntil > now) {
      return;
    }

    current.failures += 1;
    if (current.failures >= this.maxFailures) {
      current.blockedUntil = now + this.blockMs;
    }
    this.records.set(ip, current);
  }

  recordSuccess(ip: string): void {
    this.records.delete(ip);
  }
}
