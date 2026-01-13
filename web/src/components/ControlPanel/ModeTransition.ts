import type { Mode } from '../../types';
import { createEl, clearChildren } from '../../utils/dom';
import { nextFrame, waitForTransitionEnd } from '../../utils/animation';

const MODE_TRANSITION_MS = 350;

export interface ModeTransitionOptions {
  container: HTMLElement;
  getUpstreams: () => string[];
  onApply: (mode: Mode, tag: string | null) => void;
  onRestart: () => void;
  onModeChange: (mode: Mode, tag: string | null) => void;
  onTransitionStart?: () => void;
  onTransitionEnd?: () => void;
}

export class ModeTransition {
  private container: HTMLElement;
  private mode: Mode = 'auto';
  private upstreams: string[] = [];
  private selectedUpstream: string | null = null;
  private content: HTMLElement | null = null;
  private modeSelect: HTMLSelectElement | null = null;
  private upstreamSelect: HTMLSelectElement | null = null;
  private applyButton: HTMLButtonElement | null = null;
  private restartButton: HTMLButtonElement | null = null;
  private isTransitioning = false;
  private opts: ModeTransitionOptions;

  constructor(options: ModeTransitionOptions) {
    this.container = options.container;
    this.opts = options;
    this.upstreams = options.getUpstreams();
    this.renderInitial();
  }

  setUpstreams(tags: string[]): void {
    this.upstreams = tags;
    if (this.mode === 'manual') {
      this.selectedUpstream = tags[0] || null;
      this.renderContent(this.mode, false);
    }
  }

  setMode(mode: Mode, selectedUpstream: string | null, animate = false): void {
    if (animate) {
      void this.transitionTo(mode, selectedUpstream);
      return;
    }
    this.mode = mode;
    this.selectedUpstream = selectedUpstream;
    this.renderContent(mode, false);
  }

  private renderInitial(): void {
    clearChildren(this.container);
    this.renderContent(this.mode, false);
  }

  private renderContent(mode: Mode, entering: boolean): void {
    clearChildren(this.container);
    const content = this.buildModeContent(mode);
    if (entering) {
      content.classList.add('entering');
    } else {
      content.classList.add('entered');
    }
    this.content = content;
    this.container.appendChild(content);
  }

  private buildModeContent(mode: Mode): HTMLElement {
    const content = createEl('div', 'mode-content');
    const fields = createEl('div', 'mode-fields');

    this.modeSelect = document.createElement('select');
    this.modeSelect.id = 'modeSelect';
    this.modeSelect.appendChild(createOption('auto', 'auto'));
    this.modeSelect.appendChild(createOption('manual', 'manual'));
    this.modeSelect.value = mode;
    this.modeSelect.addEventListener('change', () => {
      const nextMode = this.modeSelect?.value === 'manual' ? 'manual' : 'auto';
      if (nextMode !== this.mode) {
        void this.transitionTo(nextMode, null);
      }
    });

    fields.appendChild(createField('Mode', this.modeSelect));

    if (mode === 'manual') {
      this.upstreamSelect = document.createElement('select');
      this.upstreamSelect.id = 'upstreamSelect';
      const upstreams = this.upstreams.length > 0 ? this.upstreams : ['-'];
      for (const tag of upstreams) {
        this.upstreamSelect.appendChild(createOption(tag, tag));
      }
      const selected = this.selectedUpstream || upstreams[0] || '';
      this.upstreamSelect.value = selected;
      this.selectedUpstream = this.upstreams.length > 0 ? selected : null;
      this.upstreamSelect.addEventListener('change', () => {
        this.selectedUpstream = this.upstreamSelect?.value || null;
        this.opts.onModeChange(this.mode, this.selectedUpstream);
      });
      fields.appendChild(createField('Upstream', this.upstreamSelect));
    } else {
      this.upstreamSelect = null;
    }

    const actions = createEl('div', 'mode-actions');
    this.applyButton = createButton('Apply', false);
    this.restartButton = createButton('Restart', true);

    this.applyButton.addEventListener('click', () => {
      this.opts.onApply(this.mode, this.selectedUpstream);
    });
    this.restartButton.addEventListener('click', () => {
      this.opts.onRestart();
    });

    actions.appendChild(this.applyButton);
    actions.appendChild(this.restartButton);

    content.appendChild(fields);
    content.appendChild(actions);
    return content;
  }

  private async transitionTo(mode: Mode, selectedUpstream: string | null): Promise<void> {
    if (this.isTransitioning || mode === this.mode) {
      return;
    }
    this.isTransitioning = true;
    this.container.classList.add('is-transitioning');
    this.opts.onTransitionStart?.();

    if (this.content) {
      this.content.classList.remove('entered');
      this.content.classList.add('exiting');
      await waitForTransitionEnd(this.content, MODE_TRANSITION_MS);
    }

    this.selectedUpstream = selectedUpstream;
    if (mode === 'manual') {
      this.selectedUpstream = this.upstreams[0] || null;
    } else {
      this.selectedUpstream = null;
    }

    this.renderContent(mode, true);
    await nextFrame();
    if (this.content) {
      this.content.classList.remove('entering');
      this.content.classList.add('entered');
      await waitForTransitionEnd(this.content, MODE_TRANSITION_MS);
    }

    this.mode = mode;
    this.container.classList.remove('is-transitioning');
    this.opts.onModeChange(this.mode, this.selectedUpstream);
    this.opts.onTransitionEnd?.();

    this.emphasizeApply();
    if (this.mode === 'manual' && this.upstreamSelect) {
      this.upstreamSelect.focus();
    } else if (this.modeSelect) {
      this.modeSelect.focus();
    }

    this.isTransitioning = false;
  }

  setBusy(isBusy: boolean): void {
    if (this.modeSelect) {
      this.modeSelect.disabled = isBusy;
    }
    if (this.upstreamSelect) {
      this.upstreamSelect.disabled = isBusy;
    }
    if (this.applyButton) {
      this.applyButton.disabled = isBusy;
    }
    if (this.restartButton) {
      this.restartButton.disabled = isBusy;
    }
  }

  private emphasizeApply(): void {
    if (!this.applyButton) {
      return;
    }
    this.applyButton.classList.add('emphasize');
    window.setTimeout(() => {
      this.applyButton?.classList.remove('emphasize');
    }, 800);
  }
}

function createField(label: string, control: HTMLElement): HTMLElement {
  const wrapper = createEl('div', 'control-field');
  const labelEl = createEl('label');
  labelEl.textContent = label;
  wrapper.appendChild(labelEl);
  wrapper.appendChild(control);
  return wrapper;
}

function createOption(value: string, label: string): HTMLOptionElement {
  const option = document.createElement('option');
  option.value = value;
  option.textContent = label;
  return option;
}

function createButton(label: string, secondary: boolean): HTMLButtonElement {
  const btn = document.createElement('button');
  btn.type = 'button';
  if (secondary) {
    btn.className = 'secondary';
  } else {
    btn.className = 'apply-button';
  }
  btn.textContent = label;
  return btn;
}
