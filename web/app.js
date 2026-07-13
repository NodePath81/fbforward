const tokenKey = 'fbforward.control_token';
const login = document.querySelector('#login');
const app = document.querySelector('#app');
const alertBox = document.querySelector('#alert');
const tokenInput = document.querySelector('#token');

function showAlert(message) {
  alertBox.textContent = message;
  alertBox.hidden = !message;
}

function setAuthenticated(value) {
  login.hidden = value;
  app.hidden = !value;
}

function logout() {
  sessionStorage.removeItem(tokenKey);
  setAuthenticated(false);
  tokenInput.value = '';
  showAlert('');
}

document.querySelector('#login-form').addEventListener('submit', (event) => {
  event.preventDefault();
  sessionStorage.setItem(tokenKey, tokenInput.value);
  tokenInput.value = '';
  setAuthenticated(true);
  showAlert('');
});
document.querySelector('#logout').addEventListener('click', logout);
for (const button of document.querySelectorAll('[data-page]')) {
  button.addEventListener('click', () => {
    for (const section of document.querySelectorAll('[data-section]')) section.hidden = section.id !== `page-${button.dataset.page}`;
  });
}

setAuthenticated(Boolean(sessionStorage.getItem(tokenKey)));
