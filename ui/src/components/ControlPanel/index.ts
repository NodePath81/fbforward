import type { Mode } from '../../types';
import { ModeTransition } from './ModeTransition';
import type { Store } from '../../state/store';
import type { ToastManager } from '../Toast';

interface ControlPanelOptions {
  container: HTMLElement;
  hintEl: HTMLElement;
  authButton: HTMLButtonElement;
  store: Store;
  toast: ToastManager;
  onApply: (mode: Mode, tag: string | null) => Promise<boolean>;
  onRestart: () => Promise<boolean>;
}

export function initControlPanel(options: ControlPanelOptions) {
  const { container, hintEl, authButton, store, toast, onApply, onRestart } = options;

  const modeTransition = new ModeTransition({
    container,
    getUpstreams: () => store.getState().upstreams.map(up => up.tag),
    onApply: async (mode, tag) => {
      modeTransition.setBusy(true);
      const ok = await onApply(mode, tag);
      modeTransition.setBusy(false);
      if (ok) {
        setHint('Mode updated.');
        toast.show('Mode updated.', 'success');
      }
    },
    onRestart: async () => {
      modeTransition.setBusy(true);
      const ok = await onRestart();
      modeTransition.setBusy(false);
      if (ok) {
        setHint('Restart requested.');
        toast.show('Restart requested.', 'warning');
      }
    },
    onModeChange: (mode, tag) => {
      store.setState({
        control: {
          mode,
          selectedUpstream: tag,
          isTransitioning: store.getState().control.isTransitioning
        }
      });
    },
    onTransitionStart: () => {
      setHint('');
      store.setState({
        control: {
          ...store.getState().control,
          isTransitioning: true
        }
      });
    },
    onTransitionEnd: () => {
      store.setState({
        control: {
          ...store.getState().control,
          isTransitioning: false
        }
      });
    }
  });

  authButton.addEventListener('click', () => {
    window.location.href = '/auth';
  });

  function setHint(message: string): void {
    hintEl.textContent = message;
  }

  return {
    setHint,
    setMode(mode: Mode, selectedUpstream: string | null, animate: boolean) {
      modeTransition.setMode(mode, selectedUpstream, animate);
    },
    setUpstreams(tags: string[]): void {
      modeTransition.setUpstreams(tags);
    }
  };
}
