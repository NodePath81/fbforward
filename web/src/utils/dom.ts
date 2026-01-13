export function qs<T extends Element>(root: ParentNode, selector: string): T {
  const el = root.querySelector(selector);
  if (!el) {
    throw new Error(`Missing element: ${selector}`);
  }
  return el as T;
}

export function createEl<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className?: string,
  text?: string
): HTMLElementTagNameMap[K] {
  const el = document.createElement(tag);
  if (className) {
    el.className = className;
  }
  if (text !== undefined) {
    el.textContent = text;
  }
  return el;
}

export function clearChildren(el: Element): void {
  while (el.firstChild) {
    el.removeChild(el.firstChild);
  }
}
