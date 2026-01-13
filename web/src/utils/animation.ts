export function prefersReducedMotion(): boolean {
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

export function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

export async function waitForTransitionEnd(element: HTMLElement, durationMs: number): Promise<void> {
  if (prefersReducedMotion() || durationMs <= 0) {
    return;
  }
  await new Promise<void>(resolve => {
    let done = false;
    const finish = () => {
      if (done) {
        return;
      }
      done = true;
      element.removeEventListener('transitionend', onEnd);
      resolve();
    };
    const onEnd = (event: TransitionEvent) => {
      if (event.target === element) {
        finish();
      }
    };
    element.addEventListener('transitionend', onEnd);
    window.setTimeout(finish, durationMs + 50);
  });
}

export async function nextFrame(): Promise<void> {
  await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
}
