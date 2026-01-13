import { createEl } from '../../utils/dom';

export type ToastType = 'success' | 'error' | 'warning';

export function createToastManager(container: HTMLElement) {
  return {
    show(message: string, type: ToastType = 'success', timeoutMs = 2800) {
      const toast = createEl('div', `toast ${type}`);
      toast.textContent = message;
      container.appendChild(toast);
      window.setTimeout(() => {
        toast.remove();
      }, timeoutMs);
    }
  };
}

export type ToastManager = ReturnType<typeof createToastManager>;
