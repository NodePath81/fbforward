import type { QueueStatus } from '../../types';
import { clearChildren } from '../../utils/dom';
import { formatDuration, formatScheduledTime } from '../../utils/format';

export function createQueueWidget(container: HTMLElement) {
  return function render(status: QueueStatus | null): void {
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

    const depthRow = document.createElement('div');
    depthRow.className = 'queue-metric';
    const depthLabel = document.createElement('span');
    depthLabel.className = 'queue-label';
    depthLabel.textContent = 'Pending';
    const depthValue = document.createElement('span');
    depthValue.className = 'queue-value';
    depthValue.textContent = `${status.queueDepth}`;
    depthRow.appendChild(depthLabel);
    depthRow.appendChild(depthValue);
    topRow.appendChild(depthRow);

    widget.appendChild(topRow);

    if (status.skippedTotal > 0) {
      const skippedRow = document.createElement('div');
      skippedRow.className = 'queue-metric queue-metric-warn';
      const skippedLabel = document.createElement('span');
      skippedLabel.className = 'queue-label';
      skippedLabel.textContent = 'Skipped';
      const skippedValue = document.createElement('span');
      skippedValue.className = 'queue-value';
      skippedValue.textContent = `${status.skippedTotal}`;
      skippedRow.appendChild(skippedLabel);
      skippedRow.appendChild(skippedValue);
      widget.appendChild(skippedRow);
    }

    if (status.pending.length > 0) {
      const pendingSection = document.createElement('div');
      pendingSection.className = 'queue-pending';

      const pendingHeader = document.createElement('div');
      pendingHeader.className = 'queue-pending-header';
      pendingHeader.textContent = 'Pending';
      pendingSection.appendChild(pendingHeader);

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

      widget.appendChild(pendingSection);
    }

    if (status.running.length > 0) {
      const runningSection = document.createElement('div');
      runningSection.className = 'queue-running';

      const runningHeader = document.createElement('div');
      runningHeader.className = 'queue-running-header';
      runningHeader.textContent = 'Running';
      runningSection.appendChild(runningHeader);

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

      widget.appendChild(runningSection);
    }

    container.appendChild(widget);
  };
}
