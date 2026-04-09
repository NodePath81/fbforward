import type { ToastManager } from '../components/Toast';
import { queryLogEvents } from '../api/iplog';
import type {
  LogEntryType,
  LogEventQueryParams,
  LogEventQueryResult,
  LogEventRecord,
  LogEventSortBy,
  IPLogSortOrder
} from '../types';
import { clearChildren, createEl } from '../utils/dom';
import { formatBytes, formatMs } from '../utils/format';

const PAGE_SIZE = 50;

const sortOptionsByType: Record<LogEntryType, Array<{ value: LogEventSortBy; label: string }>> = {
  all: [
    { value: 'recorded_at', label: 'Timestamp' },
    { value: 'ip', label: 'Client IP' },
    { value: 'asn', label: 'ASN' },
    { value: 'country', label: 'Country' },
    { value: 'protocol', label: 'Protocol' },
    { value: 'port', label: 'Port' },
    { value: 'entry_type', label: 'Type' }
  ],
  flow: [
    { value: 'recorded_at', label: 'Timestamp' },
    { value: 'ip', label: 'Client IP' },
    { value: 'asn', label: 'ASN' },
    { value: 'country', label: 'Country' },
    { value: 'protocol', label: 'Protocol' },
    { value: 'port', label: 'Port' },
    { value: 'entry_type', label: 'Type' },
    { value: 'upstream', label: 'Upstream' },
    { value: 'bytes_up', label: 'Bytes Up' },
    { value: 'bytes_down', label: 'Bytes Down' },
    { value: 'bytes_total', label: 'Bytes Total' },
    { value: 'duration_ms', label: 'Duration' }
  ],
  rejection: [
    { value: 'recorded_at', label: 'Timestamp' },
    { value: 'ip', label: 'Client IP' },
    { value: 'asn', label: 'ASN' },
    { value: 'country', label: 'Country' },
    { value: 'protocol', label: 'Protocol' },
    { value: 'port', label: 'Port' },
    { value: 'entry_type', label: 'Type' },
    { value: 'reason', label: 'Reason' },
    { value: 'matched_rule_type', label: 'Rule Type' },
    { value: 'matched_rule_value', label: 'Rule Value' }
  ]
};

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
  protocol: '' | 'tcp' | 'udp';
  port: string;
  reason: string;
  matchedRuleType: string;
  matchedRuleValue: string;
  entryType: LogEntryType;
  sortBy: LogEventSortBy;
  sortOrder: IPLogSortOrder;
  offset: number;
  loading: boolean;
  hasQueried: boolean;
  total: number;
  records: LogEventRecord[];
}

export function createIPLogPage(container: HTMLElement, options: IPLogPageOptions): void {
  const state: IPLogPageState = {
    startTime: '',
    endTime: '',
    cidr: '',
    asn: '',
    country: '',
    protocol: '',
    port: '',
    reason: '',
    matchedRuleType: '',
    matchedRuleValue: '',
    entryType: 'all',
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
    createEl('span', 'panel-meta', 'Unified flow and rejection history over /rpc')
  );
  filtersPanel.appendChild(filtersHeader);

  const form = createEl('form', 'iplog-form');
  const typeField = createField('Record type');
  const startField = createField('Start time');
  const endField = createField('End time');
  const cidrField = createField('CIDR');
  const asnField = createField('ASN');
  const countryField = createField('Country');
  const protocolField = createField('Protocol');
  const portField = createField('Port');
  const reasonField = createField('Reason');
  const ruleTypeField = createField('Rule type');
  const ruleValueField = createField('Rule value');
  const sortByField = createField('Sort by');
  const sortOrderField = createField('Sort order');

  const entryTypeSelect = createEl('select') as HTMLSelectElement;
  entryTypeSelect.name = 'entry_type';
  appendSelectOptions(entryTypeSelect, [
    { value: 'all', label: 'All records' },
    { value: 'flow', label: 'Flow records' },
    { value: 'rejection', label: 'Rejection records' }
  ]);
  typeField.field.appendChild(entryTypeSelect);

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

  const protocolSelect = createEl('select') as HTMLSelectElement;
  protocolSelect.name = 'protocol';
  appendSelectOptions(protocolSelect, [
    { value: '', label: 'Any' },
    { value: 'tcp', label: 'TCP' },
    { value: 'udp', label: 'UDP' }
  ]);
  protocolField.field.appendChild(protocolSelect);

  const portInput = createEl('input') as HTMLInputElement;
  portInput.type = 'number';
  portInput.name = 'port';
  portInput.min = '1';
  portInput.placeholder = '9000';
  portField.field.appendChild(portInput);

  const reasonInput = createEl('input') as HTMLInputElement;
  reasonInput.type = 'text';
  reasonInput.name = 'reason';
  reasonInput.placeholder = 'firewall_deny';
  reasonField.field.appendChild(reasonInput);

  const ruleTypeInput = createEl('input') as HTMLInputElement;
  ruleTypeInput.type = 'text';
  ruleTypeInput.name = 'matched_rule_type';
  ruleTypeInput.placeholder = 'cidr';
  ruleTypeField.field.appendChild(ruleTypeInput);

  const ruleValueInput = createEl('input') as HTMLInputElement;
  ruleValueInput.type = 'text';
  ruleValueInput.name = 'matched_rule_value';
  ruleValueInput.placeholder = '10.0.0.0/8';
  ruleValueField.field.appendChild(ruleValueInput);

  const sortBySelect = createEl('select') as HTMLSelectElement;
  sortBySelect.name = 'sort_by';
  sortByField.field.appendChild(sortBySelect);

  const sortOrderSelect = createEl('select') as HTMLSelectElement;
  sortOrderSelect.name = 'sort_order';
  appendSelectOptions(sortOrderSelect, [
    { value: 'desc', label: 'Descending' },
    { value: 'asc', label: 'Ascending' }
  ]);
  sortOrderField.field.appendChild(sortOrderSelect);

  const filterGrid = createEl('div', 'iplog-filter-grid');
  filterGrid.appendChild(typeField.field);
  filterGrid.appendChild(startField.field);
  filterGrid.appendChild(endField.field);
  filterGrid.appendChild(cidrField.field);
  filterGrid.appendChild(asnField.field);
  filterGrid.appendChild(countryField.field);
  filterGrid.appendChild(protocolField.field);
  filterGrid.appendChild(portField.field);
  filterGrid.appendChild(reasonField.field);
  filterGrid.appendChild(ruleTypeField.field);
  filterGrid.appendChild(ruleValueField.field);
  filterGrid.appendChild(sortByField.field);
  filterGrid.appendChild(sortOrderField.field);

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
  for (const label of [
    'Timestamp',
    'Type',
    'Client IP',
    'ASN',
    'Org',
    'Country',
    'Protocol',
    'Port',
    'Upstream',
    'Bytes Up',
    'Bytes Down',
    'Bytes Total',
    'Duration',
    'Reason',
    'Rule Type',
    'Rule Value'
  ]) {
    headRow.appendChild(createHeader(label));
  }
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

  entryTypeSelect.addEventListener('change', () => {
    state.entryType = entryTypeSelect.value as LogEntryType;
    state.offset = 0;
    syncSortControls();
  });

  form.addEventListener('submit', event => {
    event.preventDefault();
    syncInputsToState();
    state.offset = 0;
    void runQuery();
  });

  countryInput.addEventListener('input', () => {
    countryInput.value = countryInput.value.toUpperCase();
  });

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

  syncSortControls();
  render();

  async function runQuery(): Promise<void> {
    state.loading = true;
    render();

    const resp = await queryLogEvents(options.token, buildQueryParams(state));

    state.loading = false;
    if (!resp.ok || !resp.result) {
      options.toast.show(resp.error || 'Unable to query IP log.', 'error');
      render();
      return;
    }

    applyResult(resp.result);
    render();
  }

  function applyResult(result: LogEventQueryResult): void {
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
    state.protocol = (protocolSelect.value as '' | 'tcp' | 'udp') || '';
    state.port = portInput.value.trim();
    state.reason = reasonInput.value.trim();
    state.matchedRuleType = ruleTypeInput.value.trim();
    state.matchedRuleValue = ruleValueInput.value.trim();
    state.entryType = entryTypeSelect.value as LogEntryType;
    state.sortBy = sortBySelect.value as LogEventSortBy;
    state.sortOrder = sortOrderSelect.value as IPLogSortOrder;
    countryInput.value = state.country;
  }

  function syncSortControls(): void {
    const optionsForType = sortOptionsByType[state.entryType];
    clearChildren(sortBySelect);
    for (const option of optionsForType) {
      const opt = createEl('option') as HTMLOptionElement;
      opt.value = option.value;
      opt.textContent = option.label;
      sortBySelect.appendChild(opt);
    }
    const allowed = new Set(optionsForType.map(option => option.value));
    if (!allowed.has(state.sortBy)) {
      state.sortBy = 'recorded_at';
      state.sortOrder = 'desc';
    }
    entryTypeSelect.value = state.entryType;
    sortBySelect.value = state.sortBy;
    sortOrderSelect.value = state.sortOrder;
  }

  function render(): void {
    queryButton.disabled = state.loading;
    queryButton.textContent = state.loading ? 'Running...' : 'Run query';
    prevButton.disabled = state.loading || state.offset === 0;
    nextButton.disabled = state.loading || state.offset + PAGE_SIZE >= state.total;
    sortBySelect.disabled = state.loading;
    sortOrderSelect.disabled = state.loading;

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
      cell.setAttribute('colspan', '16');
      row.appendChild(cell);
      tbody.appendChild(row);
      return;
    }

    for (const record of state.records) {
      const row = createEl('tr');
      row.appendChild(createCell(formatRecordTime(record.recorded_at)));
      row.appendChild(createCell(record.entry_type));
      row.appendChild(createCell(record.ip));
      row.appendChild(createCell(record.asn > 0 ? record.asn.toString() : '-'));
      row.appendChild(createCell(record.as_org || '-'));
      row.appendChild(createCell(record.country || '-'));
      row.appendChild(createCell(record.protocol.toUpperCase()));
      row.appendChild(createCell(record.port.toString()));
      row.appendChild(createCell(record.upstream || '-'));
      row.appendChild(createCell(record.bytes_up === null ? '-' : formatBytes(record.bytes_up)));
      row.appendChild(createCell(record.bytes_down === null ? '-' : formatBytes(record.bytes_down)));
      row.appendChild(createCell(formatBytesTotal(record)));
      row.appendChild(createCell(record.duration_ms === null ? '-' : formatMs(record.duration_ms)));
      row.appendChild(createCell(record.reason || '-'));
      row.appendChild(createCell(record.matched_rule_type || '-'));
      row.appendChild(createCell(record.matched_rule_value || '-'));
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

function createCell(text: string): HTMLTableCellElement {
  const cell = createEl('td') as HTMLTableCellElement;
  cell.textContent = text;
  return cell;
}

function appendSelectOptions(
  select: HTMLSelectElement,
  options: Array<{ value: string; label: string }>
): void {
  for (const option of options) {
    const opt = createEl('option') as HTMLOptionElement;
    opt.value = option.value;
    opt.textContent = option.label;
    select.appendChild(opt);
  }
}

function buildQueryParams(state: IPLogPageState): LogEventQueryParams {
  const params: LogEventQueryParams = {
    entry_type: state.entryType,
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

  if (state.protocol) {
    params.protocol = state.protocol;
  }

  if (state.reason) {
    params.reason = state.reason;
  }

  if (state.matchedRuleType) {
    params.matched_rule_type = state.matchedRuleType;
  }

  if (state.matchedRuleValue) {
    params.matched_rule_value = state.matchedRuleValue;
  }

  if (state.asn) {
    const parsed = Number.parseInt(state.asn, 10);
    if (Number.isFinite(parsed)) {
      params.asn = parsed;
    }
  }

  if (state.port) {
    const parsed = Number.parseInt(state.port, 10);
    if (Number.isFinite(parsed) && parsed > 0) {
      params.port = parsed;
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

function formatBytesTotal(record: LogEventRecord): string {
  if (record.bytes_up === null || record.bytes_down === null) {
    return '-';
  }
  return formatBytes(record.bytes_up + record.bytes_down);
}
