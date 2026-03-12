export interface Point {
  x: number;
  y: number;
}

export interface RTTSeries {
  key: string;
  tag: string;
  protocol: 'tcp' | 'udp';
  points: Point[];
}

export interface TrafficSeriesSnapshot {
  upload: Point[];
  download: Point[];
}

export interface ScoreSeries {
  tag: string;
  points: Point[];
}

interface SeriesBuffer {
  timestamps: number[];
  values: number[];
}

function appendSample(buffer: SeriesBuffer, timestamp: number, value: number, maxPoints: number): void {
  buffer.timestamps.push(timestamp);
  buffer.values.push(value);
  if (buffer.timestamps.length > maxPoints) {
    buffer.timestamps.shift();
    buffer.values.shift();
  }
}

export class TimeSeriesStore {
  private readonly maxPoints: number;
  private readonly rtt = new Map<string, SeriesBuffer>();
  private readonly score = new Map<string, SeriesBuffer>();
  private readonly trafficUpload: SeriesBuffer = { timestamps: [], values: [] };
  private readonly trafficDownload: SeriesBuffer = { timestamps: [], values: [] };

  constructor(maxPoints: number = 300) {
    this.maxPoints = maxPoints;
  }

  pushRTT(tag: string, protocol: 'tcp' | 'udp', timestamp: number, value: number): void {
    if (!Number.isFinite(timestamp) || !Number.isFinite(value)) {
      return;
    }
    const key = `${tag}:${protocol}`;
    let buffer = this.rtt.get(key);
    if (!buffer) {
      buffer = { timestamps: [], values: [] };
      this.rtt.set(key, buffer);
    }
    appendSample(buffer, timestamp, value, this.maxPoints);
  }

  getRTTSeries(): RTTSeries[] {
    const series: RTTSeries[] = [];
    for (const [key, buffer] of this.rtt.entries()) {
      const separator = key.lastIndexOf(':');
      const tag = separator === -1 ? key : key.slice(0, separator);
      const protocol = (separator === -1 ? 'tcp' : key.slice(separator + 1)) as 'tcp' | 'udp';
      series.push({
        key,
        tag,
        protocol,
        points: buffer.timestamps.map((x, index) => ({ x, y: buffer.values[index] }))
      });
    }
    series.sort((a, b) => {
      const tagCompare = a.tag.localeCompare(b.tag);
      if (tagCompare !== 0) {
        return tagCompare;
      }
      return a.protocol.localeCompare(b.protocol);
    });
    return series;
  }

  pushScore(tag: string, timestamp: number, value: number): void {
    if (!Number.isFinite(timestamp) || !Number.isFinite(value)) {
      return;
    }
    let buffer = this.score.get(tag);
    if (!buffer) {
      buffer = { timestamps: [], values: [] };
      this.score.set(tag, buffer);
    }
    appendSample(buffer, timestamp, value, this.maxPoints);
  }

  getScoreSeries(): ScoreSeries[] {
    const series: ScoreSeries[] = [];
    for (const [tag, buffer] of this.score.entries()) {
      series.push({
        tag,
        points: buffer.timestamps.map((x, index) => ({ x, y: buffer.values[index] }))
      });
    }
    series.sort((a, b) => a.tag.localeCompare(b.tag));
    return series;
  }

  pushTraffic(timestamp: number, uploadBytesPerSec: number, downloadBytesPerSec: number): void {
    if (
      !Number.isFinite(timestamp) ||
      !Number.isFinite(uploadBytesPerSec) ||
      !Number.isFinite(downloadBytesPerSec)
    ) {
      return;
    }
    appendSample(this.trafficUpload, timestamp, Math.max(0, uploadBytesPerSec), this.maxPoints);
    appendSample(this.trafficDownload, timestamp, Math.max(0, downloadBytesPerSec), this.maxPoints);
  }

  getTrafficSeries(): TrafficSeriesSnapshot {
    return {
      upload: this.trafficUpload.timestamps.map((x, index) => ({
        x,
        y: this.trafficUpload.values[index]
      })),
      download: this.trafficDownload.timestamps.map((x, index) => ({
        x,
        y: this.trafficDownload.values[index]
      }))
    };
  }
}

export const timeSeriesStore = new TimeSeriesStore();
