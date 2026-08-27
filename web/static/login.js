const form = document.getElementById('login-form');
const errorMsg = document.getElementById('error-message');
const loginBtn = document.getElementById('login-btn');

form.addEventListener('submit', async (event) => {
    event.preventDefault();

    const username = document.getElementById('username').value;
    const password = document.getElementById('password').value;

    loginBtn.disabled = true;
    errorMsg.classList.add('hidden');

    try {
        const response = await fetch('/api/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ username, password }),
        });

        if (response.ok) {
            window.location.href = '/';
            return;
        }

        if (response.status === 429) {
            showError('Too many failed attempts. Wait a few minutes and try again.');
        } else {
            showError('Invalid credentials');
        }
    } catch (error) {
        showError('Could not reach the server');
    }

    document.getElementById('password').value = '';
    loginBtn.disabled = false;
});

function showError(message) {
    errorMsg.textContent = message;
    errorMsg.classList.remove('hidden');
}
