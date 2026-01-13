import { callRPC } from './api/rpc';
import { createToastManager } from './components/Toast';
import { qs } from './utils/dom';

const tokenInput = qs<HTMLInputElement>(document, '#tokenInput');
const saveButton = qs<HTMLButtonElement>(document, '#saveButton');
const authHint = qs<HTMLElement>(document, '#authHint');
const toast = createToastManager(qs<HTMLElement>(document, '#toastRegion'));

const existing = localStorage.getItem('fbforward_token') || '';
tokenInput.value = existing;

saveButton.addEventListener('click', async () => {
  const token = tokenInput.value.trim();
  if (!token) {
    authHint.textContent = 'Token is required.';
    toast.show('Token is required.', 'warning');
    return;
  }
  saveButton.disabled = true;
  authHint.textContent = 'Checking token...';
  const ok = await validateToken(token);
  if (!ok) {
    authHint.textContent = 'Token invalid.';
    toast.show('Token invalid.', 'error');
    saveButton.disabled = false;
    return;
  }
  localStorage.setItem('fbforward_token', token);
  window.location.href = '/';
});

async function validateToken(token: string): Promise<boolean> {
  const res = await callRPC(token, 'GetStatus', {});
  return res.ok === true;
}
