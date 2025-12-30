const tokenInput = document.getElementById('tokenInput');
const saveButton = document.getElementById('saveButton');
const authHint = document.getElementById('authHint');

const existing = localStorage.getItem('fbforward_token') || '';
tokenInput.value = existing;

saveButton.addEventListener('click', async () => {
  const token = tokenInput.value.trim();
  if (!token) {
    authHint.textContent = 'Token is required.';
    return;
  }
  saveButton.disabled = true;
  authHint.textContent = 'Checking token...';
  const ok = await validateToken(token);
  if (!ok) {
    authHint.textContent = 'Token invalid.';
    saveButton.disabled = false;
    return;
  }
  localStorage.setItem('fbforward_token', token);
  window.location.href = '/';
});

async function validateToken(token) {
  try {
    const res = await fetch('/rpc', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer ' + token
      },
      body: JSON.stringify({ method: 'GetStatus', params: {} })
    });
    if (!res.ok) {
      return false;
    }
    const payload = await res.json();
    return payload && payload.ok === true;
  } catch (err) {
    return false;
  }
}
