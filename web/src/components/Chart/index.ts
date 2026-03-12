export interface ChartPoint {
  x: number;
  y: number;
}

export interface ChartSeries {
  label: string;
  color: string;
  dashed?: boolean;
  points: ChartPoint[];
}

export interface ChartOptions {
  emptyLabel: string;
  yFormatter: (value: number) => string;
  xFormatter?: (value: number) => string;
  baselineZero?: boolean;
}

export interface ChartHandle {
  update: (series: ChartSeries[]) => void;
  destroy: () => void;
}

const SVG_NS = 'http://www.w3.org/2000/svg';

export function createChart(container: HTMLElement, options: ChartOptions): ChartHandle {
  const frame = document.createElement('div');
  frame.className = 'chart-frame';
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.classList.add('chart-svg');
  const legend = document.createElement('div');
  legend.className = 'chart-legend';
  const meta = document.createElement('div');
  meta.className = 'chart-meta';
  frame.appendChild(svg);
  frame.appendChild(legend);
  frame.appendChild(meta);
  container.appendChild(frame);

  let currentSeries: ChartSeries[] = [];
  const observer = new ResizeObserver(() => render());
  observer.observe(container);

  function render(): void {
    const width = Math.max(320, container.clientWidth || 640);
    const height = 260;
    svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
    svg.setAttribute('preserveAspectRatio', 'none');
    while (svg.firstChild) {
      svg.removeChild(svg.firstChild);
    }
    legend.replaceChildren();

    for (const entry of currentSeries) {
      legend.appendChild(createLegendItem(entry));
    }

    const finitePoints = currentSeries.flatMap(series =>
      series.points.filter(point => Number.isFinite(point.x) && Number.isFinite(point.y))
    );
    if (finitePoints.length === 0) {
      svg.appendChild(makeText(width / 2, height / 2, options.emptyLabel, 'chart-empty'));
      meta.textContent = 'Waiting for samples';
      return;
    }

    const padding = { top: 16, right: 18, bottom: 30, left: 56 };
    const chartWidth = width - padding.left - padding.right;
    const chartHeight = height - padding.top - padding.bottom;
    let xMin = Math.min(...finitePoints.map(point => point.x));
    let xMax = Math.max(...finitePoints.map(point => point.x));
    let yMin = Math.min(...finitePoints.map(point => point.y));
    let yMax = Math.max(...finitePoints.map(point => point.y));

    if (options.baselineZero) {
      yMin = Math.min(0, yMin);
    }
    if (xMin === xMax) {
      xMin -= 1000;
      xMax += 1000;
    }
    if (yMin === yMax) {
      const delta = yMin === 0 ? 1 : Math.abs(yMin * 0.1);
      yMin -= delta;
      yMax += delta;
    }

    const plot = document.createElementNS(SVG_NS, 'g');
    svg.appendChild(plot);

    const gridLines = 4;
    for (let i = 0; i <= gridLines; i += 1) {
      const ratio = i / gridLines;
      const y = padding.top + chartHeight * ratio;
      const value = yMax - (yMax - yMin) * ratio;
      const line = document.createElementNS(SVG_NS, 'line');
      line.setAttribute('x1', String(padding.left));
      line.setAttribute('x2', String(width - padding.right));
      line.setAttribute('y1', String(y));
      line.setAttribute('y2', String(y));
      line.setAttribute('class', 'chart-grid-line');
      plot.appendChild(line);
      plot.appendChild(makeText(padding.left - 8, y + 4, options.yFormatter(value), 'chart-axis-label', 'end'));
    }

    const xTicks = [xMin, xMin + (xMax - xMin) / 2, xMax];
    xTicks.forEach((value, index) => {
      const x = padding.left + (chartWidth * index) / (xTicks.length - 1);
      plot.appendChild(
        makeText(
          x,
          height - 8,
          options.xFormatter ? options.xFormatter(value) : new Date(value).toLocaleTimeString(),
          'chart-axis-label',
          index === 0 ? 'start' : index === xTicks.length - 1 ? 'end' : 'middle'
        )
      );
    });

    const outline = document.createElementNS(SVG_NS, 'rect');
    outline.setAttribute('x', String(padding.left));
    outline.setAttribute('y', String(padding.top));
    outline.setAttribute('width', String(chartWidth));
    outline.setAttribute('height', String(chartHeight));
    outline.setAttribute('class', 'chart-outline');
    plot.appendChild(outline);

    for (const series of currentSeries) {
      const pathData = buildPath(series.points, {
        xMin,
        xMax,
        yMin,
        yMax,
        left: padding.left,
        top: padding.top,
        width: chartWidth,
        height: chartHeight
      });
      if (!pathData) {
        continue;
      }
      const path = document.createElementNS(SVG_NS, 'path');
      path.setAttribute('d', pathData);
      path.setAttribute('fill', 'none');
      path.setAttribute('stroke', series.color);
      path.setAttribute('stroke-width', '2.5');
      path.setAttribute('stroke-linejoin', 'round');
      path.setAttribute('stroke-linecap', 'round');
      if (series.dashed) {
        path.setAttribute('stroke-dasharray', '7 5');
      }
      plot.appendChild(path);
    }

    meta.textContent = `${finitePoints.length} samples · latest ${new Date(xMax).toLocaleTimeString()}`;
  }

  render();

  return {
    update(series: ChartSeries[]) {
      currentSeries = series;
      render();
    },
    destroy() {
      observer.disconnect();
      container.replaceChildren();
    }
  };
}

function buildPath(
  points: ChartPoint[],
  bounds: {
    xMin: number;
    xMax: number;
    yMin: number;
    yMax: number;
    left: number;
    top: number;
    width: number;
    height: number;
  }
): string {
  const segments: string[] = [];
  let open = false;
  for (const point of points) {
    if (!Number.isFinite(point.x) || !Number.isFinite(point.y)) {
      open = false;
      continue;
    }
    const x = bounds.left + ((point.x - bounds.xMin) / (bounds.xMax - bounds.xMin)) * bounds.width;
    const y =
      bounds.top +
      bounds.height -
      ((point.y - bounds.yMin) / (bounds.yMax - bounds.yMin)) * bounds.height;
    segments.push(`${open ? 'L' : 'M'}${x.toFixed(2)},${y.toFixed(2)}`);
    open = true;
  }
  return segments.join(' ');
}

function createLegendItem(series: ChartSeries): HTMLElement {
  const item = document.createElement('div');
  item.className = 'chart-legend-item';
  const swatch = document.createElement('span');
  swatch.className = `chart-legend-swatch${series.dashed ? ' is-dashed' : ''}`;
  swatch.style.setProperty('--series-color', series.color);
  const label = document.createElement('span');
  label.textContent = series.label;
  item.appendChild(swatch);
  item.appendChild(label);
  return item;
}

function makeText(
  x: number,
  y: number,
  value: string,
  className: string,
  anchor: 'start' | 'middle' | 'end' = 'middle'
): SVGTextElement {
  const text = document.createElementNS(SVG_NS, 'text');
  text.setAttribute('x', String(x));
  text.setAttribute('y', String(y));
  text.setAttribute('text-anchor', anchor);
  text.setAttribute('class', className);
  text.textContent = value;
  return text;
}
