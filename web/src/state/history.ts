export interface SessionHistoryEntry {
  id: string;
  kind: 'tcp' | 'udp';
  clientAddr: string;
  upstream: string;
  startTime: number;
  startApproximate: boolean;
  endTime: number;
  endApproximate: boolean;
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
  startApproximate: boolean;
  bytesUp: number;
  bytesDown: number;
  segmentsUp: number;
  segmentsDown: number;
}

export class HistoryStore {
  private sessions: SessionHistoryEntry[] = [];
  private activeMap: Map<string, ActiveSession> = new Map();

  trackSessionStart(
    id: string,
    kind: 'tcp' | 'udp',
    clientAddr: string,
    upstream: string,
    startTime: number,
    startApproximate: boolean
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
      startApproximate,
      bytesUp: 0,
      bytesDown: 0,
      segmentsUp: 0,
      segmentsDown: 0
    });
  }

  trackSessionUpdate(
    id: string,
    bytesUp: number,
    bytesDown: number,
    segmentsUp: number,
    segmentsDown: number
  ): void {
    const entry = this.activeMap.get(id);
    if (!entry) {
      return;
    }
    entry.bytesUp = bytesUp;
    entry.bytesDown = bytesDown;
    entry.segmentsUp = segmentsUp;
    entry.segmentsDown = segmentsDown;
  }

  trackSessionEnd(id: string, endTime?: number): void {
    const entry = this.activeMap.get(id);
    if (!entry) {
      return;
    }
    this.activeMap.delete(id);
    this.sessions.unshift({
      ...entry,
      endTime: endTime ?? Date.now(),
      endApproximate: endTime === undefined
    });
    if (this.sessions.length > 1000) {
      this.sessions.length = 1000;
    }
  }

  getSessions(): SessionHistoryEntry[] {
    return this.sessions;
  }
}

export const historyStore = new HistoryStore();
