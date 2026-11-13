import {byId} from './dom.js';
// Restart the soundbar (extension bar91.system), at the bottom of the System
// monitor: rarely needed, so out of the way, and confirmed by a second tap
// rather than a dialog. The page goes offline, then reconnects by itself.
const CONFIRM_MS = 5000;
export function createRestart(root, {request}) {
  const $ = id => byId(root, id);
  const button = $('restart'), note = $('restart-note');
  let latest = null, armed = false, timer = null, sent = false, away = false;
  // The bar may be unavailable (that is when a restart helps): only the
  // connection to the remote and a running control hold it back.
  const busy = () => !latest?.online || !!latest?.active;
  function render(context) {
    latest = context;
    if (context.active === 'reboot') sent = true;
    button.disabled = busy() && !sent;
    button.classList.toggle('confirm', armed);
    button.textContent = sent ? 'Restarting…' : armed ? 'Tap again to restart' : 'Restart…';
    note.textContent = sent ? 'The soundbar is restarting. This page reconnects by itself in about a minute.' : armed ? 'The sound stops and the bar restarts. Tap again within 5 seconds to confirm.' : 'Only if it stops responding. The sound stops; the bar is back in about a minute, and this page reconnects by itself.';
    // Once the page has lost the remote and found it again, the restart is over.
    if (sent && !context.online) away = true;
    if (sent && away && context.online) { sent = false; away = false; render(context); }
  }
  const refresh = () => { if (latest) render(latest); };
  button.addEventListener('click', () => {
    if (sent || busy()) return;
    if (!armed) {
      armed = true;
      clearTimeout(timer);
      timer = setTimeout(() => { armed = false; refresh(); }, CONFIRM_MS);
      refresh();
      return;
    }
    armed = false;
    clearTimeout(timer);
    if (request('reboot') !== false) sent = true;
    refresh();
  });
  return {
    render,
    parameters(action) { return action === 'reboot' ? {confirm: true} : null; },
  };
}
