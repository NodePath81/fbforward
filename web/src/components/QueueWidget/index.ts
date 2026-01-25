import type { QueueStatus } from '../../types';
import { clearChildren } from '../../utils/dom';
import { formatDuration, formatScheduledTime } from '../../utils/format';

export function createQueueWidget(container: HTMLElement) {
  return function render(status: QueueStatus | null): void {
    const previousBody = container.querySelector<HTMLElement>('.queue-body');
    const previousScrollTop = previousBody?.scrollTop ?? 0;
    clearChildren(container);

    if (!status) {
      const emptyWidget = document.createElement('div');
      emptyWidget.className = 'queue-widget';

      const emptyHeader = document.createElement('div');
      emptyHeader.className = 'queue-header';
      emptyHeader.textContent = 'Queue';
      emptyWidget.appendChild(emptyHeader);

      const emptyState = document.createElement('div');
      emptyState.className = 'queue-empty';
      emptyState.textContent = 'No data';
      emptyWidget.appendChild(emptyState);

      container.appendChild(emptyWidget);
      return;
    }

    const widget = document.createElement('div');
    widget.className = 'queue-widget';

    const topRow = document.createElement('div');
    topRow.className = 'queue-top';

    const header = document.createElement('div');
    header.className = 'queue-header';
    header.textContent = 'Queue';
    topRow.appendChild(header);

    const summary = document.createElement('div');
    summary.className = 'queue-summary';

    const pendingStat = document.createElement('div');
    pendingStat.className = 'queue-stat';
    const pendingLabel = document.createElement('span');
    pendingLabel.className = 'queue-label';
    pendingLabel.textContent = 'Pending';
    const pendingValue = document.createElement('span');
    pendingValue.className = 'queue-value';
    pendingValue.textContent = `${status.queueDepth}`;
    pendingStat.appendChild(pendingLabel);
    pendingStat.appendChild(pendingValue);
    summary.appendChild(pendingStat);

    const skippedStat = document.createElement('div');
    skippedStat.className = 'queue-stat';
    const skippedLabel = document.createElement('span');
    skippedLabel.className = 'queue-label';
    skippedLabel.textContent = 'Skipped';
    const skippedValue = document.createElement('span');
    skippedValue.className = 'queue-value';
    skippedValue.textContent = `${status.skippedTotal}`;
    skippedStat.appendChild(skippedLabel);
    skippedStat.appendChild(skippedValue);
    summary.appendChild(skippedStat);

    topRow.appendChild(summary);

    widget.appendChild(topRow);

    const body = document.createElement('div');
    body.className = 'queue-body';

    const runningSection = document.createElement('div');
    runningSection.className = 'queue-section queue-running';

    const runningHeader = document.createElement('div');
    runningHeader.className = 'queue-running-header';
    runningHeader.textContent = 'Running';
    runningSection.appendChild(runningHeader);

    if (status.running.length > 0) {
      for (const test of status.running) {
        const testItem = document.createElement('div');
        testItem.className = 'queue-test-item';
        const elapsed = formatDuration(test.elapsedMs / 1000);

        const upstream = document.createElement('div');
        upstream.className = 'queue-test-upstream';
        upstream.textContent = test.upstream;
        testItem.appendChild(upstream);

        const details = document.createElement('div');
        details.className = 'queue-test-details';

        const protocol = document.createElement('span');
        protocol.className = 'queue-test-protocol';
        protocol.textContent = test.protocol.toUpperCase();
        details.appendChild(protocol);

        const sep1 = document.createElement('span');
        sep1.className = 'queue-test-separator';
        sep1.textContent = '·';
        details.appendChild(sep1);

        const direction = document.createElement('span');
        direction.className = 'queue-test-direction';
        direction.textContent = test.direction;
        details.appendChild(direction);

        const sep2 = document.createElement('span');
        sep2.className = 'queue-test-separator';
        sep2.textContent = '·';
        details.appendChild(sep2);

        const elapsedEl = document.createElement('span');
        elapsedEl.className = 'queue-test-elapsed';
        elapsedEl.textContent = elapsed;
        details.appendChild(elapsedEl);

        testItem.appendChild(details);
        runningSection.appendChild(testItem);
      }
    } else {
      const empty = document.createElement('div');
      empty.className = 'queue-section-empty';
      empty.textContent = 'No running tests';
      runningSection.appendChild(empty);
    }

    body.appendChild(runningSection);

    const pendingSection = document.createElement('div');
    pendingSection.className = 'queue-section queue-pending';

    const pendingHeader = document.createElement('div');
    pendingHeader.className = 'queue-pending-header';
    pendingHeader.textContent = 'Pending';
    pendingSection.appendChild(pendingHeader);

    if (status.pending.length > 0) {
      for (const item of status.pending) {
        const pendingItem = document.createElement('div');
        pendingItem.className = 'queue-pending-item';

        const upstream = document.createElement('div');
        upstream.className = 'queue-pending-upstream';
        upstream.textContent = item.upstream;
        pendingItem.appendChild(upstream);

        const details = document.createElement('div');
        details.className = 'queue-pending-details';

        const protocol = document.createElement('span');
        protocol.className = 'queue-pending-protocol';
        protocol.textContent = item.protocol.toUpperCase();
        details.appendChild(protocol);

        const sep1 = document.createElement('span');
        sep1.className = 'queue-pending-separator';
        sep1.textContent = '·';
        details.appendChild(sep1);

        const direction = document.createElement('span');
        direction.className = 'queue-pending-direction';
        direction.textContent = item.direction;
        details.appendChild(direction);

        const sep2 = document.createElement('span');
        sep2.className = 'queue-pending-separator';
        sep2.textContent = '·';
        details.appendChild(sep2);

        const scheduledTime = document.createElement('span');
        scheduledTime.className = 'queue-pending-time';
        scheduledTime.textContent = formatScheduledTime(item.scheduledAt);
        details.appendChild(scheduledTime);

        pendingItem.appendChild(details);
        pendingSection.appendChild(pendingItem);
      }
    } else {
      const empty = document.createElement('div');
      empty.className = 'queue-section-empty';
      empty.textContent = 'No pending tests';
      pendingSection.appendChild(empty);
    }

    body.appendChild(pendingSection);

    widget.appendChild(body);
    container.appendChild(widget);
    if (previousScrollTop > 0) {
      body.scrollTop = Math.min(previousScrollTop, body.scrollHeight - body.clientHeight);
    }
  };
}
