// ── trakt auth ────────────────────────────────────────────────────────────────

let traktPollTimer = null;
let traktCountdownTimer = null;
let traktExpiresAt = null;

async function traktStartAuth() {
  const clientID     = document.getElementById('trakt-client-id').value.trim();
  const clientSecret = document.getElementById('trakt-client-secret').value.trim();
  if (!clientID || !clientSecret) {
    traktSetStatus('error', '<strong>Client ID and Client Secret are required.</strong>');
    return;
  }

  clearInterval(traktPollTimer);
  clearInterval(traktCountdownTimer);

  const btn = document.getElementById('trakt-auth-btn');
  btn.disabled = true;
  btn.textContent = 'Starting…';
  traktSetStatus('', '');

  try {
    const r = await fetch('/api/trakt/auth/start', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({client_id: clientID, client_secret: clientSecret}),
    });
    if (!r.ok) {
      const msg = await r.text();
      traktSetStatus('error', '<strong>Error:</strong> ' + esc(msg));
      btn.disabled = false;
      btn.textContent = 'Authorize';
      return;
    }
    const { user_code, verification_url, expires_in } = await r.json();
    traktExpiresAt = Date.now() + expires_in * 1000;

    traktSetStatus('pending', `
      <div>Visit the link below and enter your code:</div>
      <div class="auth-url"><a href="${esc(verification_url)}" target="_blank" rel="noopener">${esc(verification_url)}</a></div>
      <div class="auth-code">${esc(user_code)}</div>
      <div class="auth-countdown" id="trakt-countdown"></div>
    `);

    traktCountdownTimer = setInterval(() => {
      const secs = Math.max(0, Math.round((traktExpiresAt - Date.now()) / 1000));
      const el = document.getElementById('trakt-countdown');
      if (el) el.textContent = 'Expires in ' + secs + 's';
      if (secs <= 0) clearInterval(traktCountdownTimer);
    }, 1000);

    traktPollTimer = setInterval(traktPoll, 3000);
  } catch (e) {
    traktSetStatus('error', '<strong>Error:</strong> ' + esc(String(e)));
    btn.disabled = false;
    btn.textContent = 'Authorize';
  }
}

async function traktPoll() {
  try {
    const r = await fetch('/api/trakt/auth/poll');
    if (!r.ok) return;
    const { status, message } = await r.json();

    if (status === 'authorized') {
      clearInterval(traktPollTimer);
      clearInterval(traktCountdownTimer);
      traktSetStatus('authorized', '✓ Authorization successful — token saved to database.');
      const btn = document.getElementById('trakt-auth-btn');
      btn.disabled = false;
      btn.textContent = 'Authorize';
    } else if (status === 'error') {
      clearInterval(traktPollTimer);
      clearInterval(traktCountdownTimer);
      traktSetStatus('error', '<strong>Error:</strong> ' + esc(message || 'unknown error'));
      const btn = document.getElementById('trakt-auth-btn');
      btn.disabled = false;
      btn.textContent = 'Authorize';
    }
  } catch (_) {}
}

function traktSetStatus(type, html) {
  const el = document.getElementById('trakt-auth-status');
  const body = document.getElementById('trakt-auth-body');
  el.className = 'auth-status' + (type ? ' ' + type : '');
  el.style.display = type ? 'block' : 'none';
  body.innerHTML = html;
}

