import type { ToastManager } from '../components/Toast';
import { queryIPLog } from '../api/iplog';
import type {
  IPLogQueryParams,
  IPLogQueryResult,
  IPLogRecord,
  IPLogSortBy,
  IPLogSortOrder
} from '../types';
import { clearChildren, createEl } from '../utils/dom';
import { formatBytes, formatMs } from '../utils/format';

const PAGE_SIZE = 50;

interface IPLogPageOptions {
  token: string;
  toast: ToastManager;
}

interface IPLogPageState {
  startTime: string;
  endTime: string;
  cidr: string;
  asn: string;
  country: string;
  sortBy: IPLogSortBy;
  sortOrder: IPLogSortOrder;
  offset: number;
  loading: boolean;
  hasQueried: boolean;
  total: number;
  records: IPLogRecord[];
}

type SortButtonState = Record<IPLogSortBy, HTMLButtonElement>;

export function createIPLogPage(container: HTMLElement, options: IPLogPageOptions): void {
  const state: IPLogPageState = {
    startTime: '',
    endTime: '',
    cidr: '',
    asn: '',
    country: '',
    sortBy: 'recorded_at',
    sortOrder: 'desc',
    offset: 0,
    loading: false,
    hasQueried: false,
    total: 0,
    records: []
  };

  clearChildren(container);

  const filtersPanel = createEl('section', 'panel');
  const filtersHeader = createEl('div', 'panel-header');
  filtersHeader.appendChild(createEl('h2', '', 'IP Log'));
  filtersHeader.appendChild(
    createEl('span', 'panel-meta', 'Persisted IP-log queries over /rpc')
  );
  filtersPanel.appendChild(filtersHeader);

  const form = createEl('form', 'iplog-form');
  const startField = createField('Start time');
  const endField = createField('End time');
  const cidrField = createField('CIDR');
  const asnField = createField('ASN');
  const countryField = createField('Country');

  const startInput = createEl('input') as HTMLInputElement;
  startInput.type = 'datetime-local';
  startInput.name = 'start_time';
  startField.field.appendChild(startInput);

  const endInput = createEl('input') as HTMLInputElement;
  endInput.type = 'datetime-local';
  endInput.name = 'end_time';
  endField.field.appendChild(endInput);

  const cidrInput = createEl('input') as HTMLInputElement;
  cidrInput.type = 'text';
  cidrInput.name = 'cidr';
  cidrInput.placeholder = '10.0.0.0/24';
  cidrField.field.appendChild(cidrInput);

  const asnInput = createEl('input') as HTMLInputElement;
  asnInput.type = 'number';
  asnInput.name = 'asn';
  asnInput.min = '0';
  asnInput.placeholder = '13335';
  asnField.field.appendChild(asnInput);

  const countryInput = createEl('input') as HTMLInputElement;
  countryInput.type = 'text';
  countryInput.name = 'country';
  countryInput.placeholder = 'US';
  countryInput.maxLength = 2;
  countryField.field.appendChild(countryInput);

  const filterGrid = createEl('div', 'iplog-filter-grid');
  filterGrid.appendChild(startField.field);
  filterGrid.appendChild(endField.field);
  filterGrid.appendChild(cidrField.field);
  filterGrid.appendChild(asnField.field);
  filterGrid.appendChild(countryField.field);

  const actions = createEl('div', 'iplog-actions');
  const queryButton = createEl('button', '', 'Run query') as HTMLButtonElement;
  queryButton.type = 'submit';
  actions.appendChild(queryButton);

  form.appendChild(filterGrid);
  form.appendChild(actions);
  filtersPanel.appendChild(form);

  const resultsPanel = createEl('section', 'panel');
  const resultsHeader = createEl('div', 'panel-header');
  resultsHeader.appendChild(createEl('h2', '', 'Results'));
  const summary = createEl('span', 'panel-meta', 'Awaiting query');
  resultsHeader.appendChild(summary);
  resultsPanel.appendChild(resultsHeader);

  const resultsBody = createEl('div', 'iplog-results');
  const statusLine = createEl('p', 'hint', 'Run a query to load persisted IP logs.');
  const tableWrap = createEl('div', 'table-wrap');
  const table = createEl('table');
  const thead = createEl('thead');
  const headRow = createEl('tr');
  const sortButtons: SortButtonState = {
    recorded_at: createSortButton('Timestamp', 'recorded_at'),
    bytes_up: createSortButton('Bytes Up', 'bytes_up'),
    bytes_down: createSortButton('Bytes Down', 'bytes_down'),
    bytes_total: createSortButton('Bytes Total', 'bytes_total'),
    duration_ms: createSortButton('Duration', 'duration_ms')
  };

  headRow.appendChild(createSortableHeader(sortButtons.recorded_at));
  headRow.appendChild(createHeader('Client IP'));
  headRow.appendChild(createHeader('ASN'));
  headRow.appendChild(createHeader('Org'));
  headRow.appendChild(createHeader('Country'));
  headRow.appendChild(createHeader('Protocol'));
  headRow.appendChild(createHeader('Upstream'));
  headRow.appendChild(createHeader('Port'));
  headRow.appendChild(createSortableHeader(sortButtons.bytes_up));
  headRow.appendChild(createSortableHeader(sortButtons.bytes_down));
  headRow.appendChild(createSortableHeader(sortButtons.bytes_total));
  headRow.appendChild(createSortableHeader(sortButtons.duration_ms));
  thead.appendChild(headRow);
  table.appendChild(thead);

  const tbody = createEl('tbody');
  table.appendChild(tbody);
  tableWrap.appendChild(table);

  const pager = createEl('div', 'iplog-pager');
  const pagerInfo = createEl('span', 'panel-meta', 'Page 1');
  const pagerActions = createEl('div', 'iplog-pager-actions');
  const prevButton = createEl('button', 'secondary', 'Previous') as HTMLButtonElement;
  prevButton.type = 'button';
  const nextButton = createEl('button', 'secondary', 'Next') as HTMLButtonElement;
  nextButton.type = 'button';
  pagerActions.appendChild(prevButton);
  pagerActions.appendChild(nextButton);
  pager.appendChild(pagerInfo);
  pager.appendChild(pagerActions);

  resultsBody.appendChild(statusLine);
  resultsBody.appendChild(tableWrap);
  resultsBody.appendChild(pager);
  resultsPanel.appendChild(resultsBody);

  container.appendChild(filtersPanel);
  container.appendChild(resultsPanel);

  form.addEventListener('submit', event => {
    event.preventDefault();
    syncInputsToState();
    state.offset = 0;
    void runQuery();
  });

  countryInput.addEventListener('input', () => {
    countryInput.value = countryInput.value.toUpperCase();
  });

  for (const [sortBy, button] of Object.entries(sortButtons) as Array<[IPLogSortBy, HTMLButtonElement]>) {
    button.addEventListener('click', () => {
      if (state.sortBy === sortBy) {
        state.sortOrder = state.sortOrder === 'asc' ? 'desc' : 'asc';
      } else {
        state.sortBy = sortBy;
        state.sortOrder = 'desc';
      }
      state.offset = 0;
      updateSortIndicators(sortButtons, state.sortBy, state.sortOrder);
      void runQuery();
    });
  }

  prevButton.addEventListener('click', () => {
    if (state.loading || state.offset === 0) {
      return;
    }
    state.offset = Math.max(0, state.offset - PAGE_SIZE);
    void runQuery();
  });

  nextButton.addEventListener('click', () => {
    if (state.loading || state.offset + PAGE_SIZE >= state.total) {
      return;
    }
    state.offset += PAGE_SIZE;
    void runQuery();
  });

  updateSortIndicators(sortButtons, state.sortBy, state.sortOrder);
  render();

  async function runQuery(): Promise<void> {
    state.loading = true;
    render();

    const resp = await queryIPLog(options.token, buildQueryParams(state));

    state.loading = false;
    if (!resp.ok || !resp.result) {
      options.toast.show(resp.error || 'Unable to query IP log.', 'error');
      render();
      return;
    }

    applyResult(resp.result);
    render();
  }

  function applyResult(result: IPLogQueryResult): void {
    state.hasQueried = true;
    state.total = result.total;
    state.records = result.records;
  }

  function syncInputsToState(): void {
    state.startTime = startInput.value;
    state.endTime = endInput.value;
    state.cidr = cidrInput.value.trim();
    state.asn = asnInput.value.trim();
    state.country = countryInput.value.trim().toUpperCase();
    countryInput.value = state.country;
  }

  function render(): void {
    queryButton.disabled = state.loading;
    queryButton.textContent = state.loading ? 'Running...' : 'Run query';
    prevButton.disabled = state.loading || state.offset === 0;
    nextButton.disabled = state.loading || state.offset + PAGE_SIZE >= state.total;

    if (!state.hasQueried) {
      summary.textContent = 'Awaiting query';
      statusLine.textContent = state.loading
        ? 'Query in progress...'
        : 'Run a query to load persisted IP logs.';
      clearChildren(tbody);
      pagerInfo.textContent = 'Page 1';
      return;
    }

    const start = state.total === 0 ? 0 : state.offset + 1;
    const end = Math.min(state.offset + state.records.length, state.total);
    summary.textContent = `${state.total.toLocaleString()} total results`;
    statusLine.textContent = state.loading
      ? `Refreshing ${start}-${end} of ${state.total.toLocaleString()}...`
      : state.total === 0
        ? 'No records matched this query.'
        : `Showing ${start}-${end} of ${state.total.toLocaleString()} records`;
    pagerInfo.textContent = state.total === 0
      ? 'Page 1'
      : `Page ${Math.floor(state.offset / PAGE_SIZE) + 1}`;

    clearChildren(tbody);
    if (state.records.length === 0) {
      const row = createEl('tr');
      const cell = createEl('td', 'empty-row', 'No records matched this query.');
      cell.setAttribute('colspan', '12');
      row.appendChild(cell);
      tbody.appendChild(row);
      return;
    }

    for (const record of state.records) {
      const row = createEl('tr');
      row.appendChild(createCell(formatRecordTime(record.recorded_at)));
      row.appendChild(createCell(record.ip));
      row.appendChild(createCell(record.asn > 0 ? record.asn.toString() : '-'));
      row.appendChild(createCell(record.as_org || '-'));
      row.appendChild(createCell(record.country || '-'));
      row.appendChild(createCell(record.protocol.toUpperCase()));
      row.appendChild(createCell(record.upstream || '-'));
      row.appendChild(createCell(record.port.toString()));
      row.appendChild(createCell(formatBytes(record.bytes_up)));
      row.appendChild(createCell(formatBytes(record.bytes_down)));
      row.appendChild(createCell(formatBytes(record.bytes_up + record.bytes_down)));
      row.appendChild(createCell(formatMs(record.duration_ms)));
      tbody.appendChild(row);
    }
  }
}

function createField(label: string): { field: HTMLElement } {
  const field = createEl('label', 'control-field');
  field.appendChild(createEl('span', '', label));
  return { field };
}

function createHeader(label: string): HTMLTableCellElement {
  const th = createEl('th') as HTMLTableCellElement;
  th.textContent = label;
  return th;
}

function createSortableHeader(button: HTMLButtonElement): HTMLTableCellElement {
  const th = createEl('th') as HTMLTableCellElement;
  th.appendChild(button);
  th.setAttribute('aria-sort', 'none');
  return th;
}

function createSortButton(label: string, sortBy: IPLogSortBy): HTMLButtonElement {
  const button = createEl('button', 'sort-button', label) as HTMLButtonElement;
  button.type = 'button';
  button.dataset.sort = sortBy;
  button.appendChild(createEl('span', 'sort-indicator'));
  return button;
}

function updateSortIndicators(
  buttons: SortButtonState,
  activeSortBy: IPLogSortBy,
  activeSortOrder: IPLogSortOrder
): void {
  for (const [sortBy, button] of Object.entries(buttons) as Array<[IPLogSortBy, HTMLButtonElement]>) {
    const active = sortBy === activeSortBy;
    button.classList.toggle('is-active', active);
    const indicator = button.querySelector<HTMLElement>('.sort-indicator');
    if (indicator) {
      indicator.textContent = active ? (activeSortOrder === 'asc' ? '\u25B2' : '\u25BC') : '';
    }
    const th = button.closest('th');
    if (th) {
      th.setAttribute(
        'aria-sort',
        active ? (activeSortOrder === 'asc' ? 'ascending' : 'descending') : 'none'
      );
    }
  }
}

function createCell(text: string): HTMLTableCellElement {
  const cell = createEl('td') as HTMLTableCellElement;
  cell.textContent = text;
  return cell;
}

function buildQueryParams(state: IPLogPageState): IPLogQueryParams {
  const params: IPLogQueryParams = {
    sort_by: state.sortBy,
    sort_order: state.sortOrder,
    limit: PAGE_SIZE,
    offset: state.offset
  };

  const startTime = parseDateTimeLocal(state.startTime);
  if (startTime !== null) {
    params.start_time = startTime;
  }

  const endTime = parseDateTimeLocal(state.endTime);
  if (endTime !== null) {
    params.end_time = endTime;
  }

  if (state.cidr) {
    params.cidr = state.cidr;
  }

  if (state.country) {
    params.country = state.country;
  }

  if (state.asn) {
    const parsed = Number.parseInt(state.asn, 10);
    if (Number.isFinite(parsed)) {
      params.asn = parsed;
    }
  }

  return params;
}

function parseDateTimeLocal(value: string): number | null {
  if (!value) {
    return null;
  }
  const timestamp = new Date(value).getTime();
  if (!Number.isFinite(timestamp)) {
    return null;
  }
  return Math.floor(timestamp / 1000);
}

function formatRecordTime(value: number): string {
  if (!Number.isFinite(value) || value <= 0) {
    return '-';
  }
  return new Date(value * 1000).toLocaleString();
}
