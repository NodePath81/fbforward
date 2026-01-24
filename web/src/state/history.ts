export interface TestHistoryEntry {
  id: string;
  timestamp: number;
  upstream: string;
  protocol: 'tcp' | 'udp';
  direction: 'upload' | 'download';
  durationMs: number;
  success: boolean;
  bandwidthUpBps?: number;
  bandwidthDownBps?: number;
  rttMs?: number;
  jitterMs?: number;
  lossRate?: number;
  retransRate?: number;
  error?: string;
}

export interface SessionHistoryEntry {
  id: string;
  kind: 'tcp' | 'udp';
  clientAddr: string;
  upstream: string;
  startTime: number;
  endTime: number;
  bytesUp: number;
  bytesDown: number;
  segmentsUp: number;
  segmentsDown: number;
}

interface ActiveSession {
  id: string;
  kind: 'tcp' | 'udp';
  clientAddr: string;
  upstream: string;
  startTime: number;
  bytesUp: number;
  bytesDown: number;
  segmentsUp: number;
  segmentsDown: number;
}

export class HistoryStore {
  private tests: TestHistoryEntry[] = [];
  private sessions: SessionHistoryEntry[] = [];
  private activeMap: Map<string, ActiveSession> = new Map();
  private testSeq = 0;

  addTest(entry: Omit<TestHistoryEntry, 'id'>): void {
    const id = `${Date.now()}-${this.testSeq++}`;
    this.tests.unshift({ id, ...entry });
    if (this.tests.length > 1000) {
      this.tests.length = 1000;
    }
  }

  trackSessionStart(
    id: string,
    kind: 'tcp' | 'udp',
    clientAddr: string,
    upstream: string,
    startTime: number
  ): void {
    if (this.activeMap.has(id)) {
      return;
    }
    this.activeMap.set(id, {
      id,
      kind,
      clientAddr,
      upstream,
      startTime,
      bytesUp: 0,
      bytesDown: 0,
      segmentsUp: 0,
      segmentsDown: 0
    });
  }

  trackSessionUpdate(id: string, bytesUp: number, bytesDown: number, segmentsUp: number, segmentsDown: number): void {
    const entry = this.activeMap.get(id);
    if (!entry) {
      return;
    }
    entry.bytesUp = bytesUp;
    entry.bytesDown = bytesDown;
    entry.segmentsUp = segmentsUp;
    entry.segmentsDown = segmentsDown;
  }

  trackSessionEnd(id: string): void {
    const entry = this.activeMap.get(id);
    if (!entry) {
      return;
    }
    this.activeMap.delete(id);
    this.sessions.unshift({
      ...entry,
      endTime: Date.now()
    });
    if (this.sessions.length > 1000) {
      this.sessions.length = 1000;
    }
  }

  getTests(): TestHistoryEntry[] {
    return this.tests;
  }

  getSessions(): SessionHistoryEntry[] {
    return this.sessions;
  }
}

export const historyStore = new HistoryStore();
