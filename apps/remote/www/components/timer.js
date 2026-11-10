import {byId} from './dom.js';
// Timers on the front display (extension bar91.timer). They work with the bar
// on or in standby; when one ends, its text shows until a key of the bar's
// remote is pressed, it is cancelled here, or 10 minutes pass.
const MAX_MINUTES = 720;
// Like the remote: upper case, no accents; letters, digits, space and - . ' (the
// characters the display shows correctly)
const fold = text => text.normalize('NFD').replace(/[̀-ͯ]/g, '').replace(/œ/gi, 'OE').replace(/æ/gi, 'AE').replace(/ß/g, 'SS').replace(/[’‘]/g, "'").replace(/[–—]/g, '-').toUpperCase().trim().split(/\s+/).join(' ');
const validText = text => text === '' || /^[A-Z0-9 .'-]{1,31}$/.test(text);
export const timerClock = ms => {
  const s = Math.max(0, Math.ceil(ms / 1000));
  const two = n => String(n).padStart(2, '0');
  return s >= 3600 ? Math.floor(s / 3600) + 'H' + two(Math.floor(s / 60) % 60) + "'" + two(s % 60) : Math.floor(s / 60) + "'" + two(s % 60);
};
export function createTimer(root, {request}) {
  const $ = id => byId(root, id);
  const text = $('timer-text'), minutes = $('timer-minutes'), countdown = $('timer-countdown'), start = $('timer-start'), list = $('timer-list');
  let latest = null, seen = '', received = 0, cancelling = null, starting = null, problem = '';
  const timers = () => latest?.state?.timers || [];
  function draft() {
    const name = fold(text.value);
    const m = Number(minutes.value);
    if (!validText(name)) return {error: "Text: letters, digits, space and - . ' only (31 at most)."};
    if (!Number.isInteger(m) || m < 1 || m > MAX_MINUTES) return {error: 'Duration: 1 to 720 minutes.'};
    const shown = name || 'TIMER';
    if (timers().some(t => t.text === shown)) return {error: 'A timer named ' + shown + ' is already running. Choose another text.'};
    if (timers().length >= 8) return {error: 'At most 8 timers at a time.'};
    return {parameters: {text: name || 'TIMER', duration_s: m * 60, countdown: countdown.checked}};
  }
  function renderList() {
    const now = Date.now();
    const items = timers();
    list.replaceChildren(...items.map(t => {
      const item = root.ownerDocument.createElement('li');
      item.className = 'timer-item' + (t.ringing ? ' ringing' : '');
      const name = root.ownerDocument.createElement('span');
      name.className = 'timer-name'; name.textContent = t.text;
      const time = root.ownerDocument.createElement('span');
      time.className = 'timer-time';
      time.textContent = t.ringing ? 'Ringing · press any key on the JBL remote' : timerClock(t.remaining_ms - (now - received)) + (t.countdown ? ' · minutes shown on the bar' : '');
      const cancel = root.ownerDocument.createElement('button');
      cancel.type = 'button'; cancel.className = 'timer-cancel'; cancel.dataset.timer = t.id;
      cancel.textContent = cancelling === t.id ? 'Cancelling…' : t.ringing ? 'Stop' : 'Cancel';
      cancel.setAttribute('aria-label', (t.ringing ? 'Stop ' : 'Cancel ') + t.text);
      cancel.disabled = !latest?.ready || !!latest?.active;
      item.append(name, time, cancel);
      return item;
    }));
    list.hidden = items.length === 0;
  }
  function render(context) {
    // remaining_ms is as of the last state from the soundbar: count from its arrival.
    const key = JSON.stringify(context.state?.timers || []);
    if (key !== seen) { seen = key; received = Date.now(); }
    latest = context;
    const {ready, active} = context;
    const d = draft();
    start.disabled = !ready || !!active || !!d.error;
    start.textContent = active === 'timer_start' ? 'Starting…' : 'Start timer';
    if (active !== 'timer_cancel') cancelling = null;
    const running = timers().length;
    $('timer-state').textContent = problem || (d.error && (text.value || minutes.value !== '5') ? d.error : !ready ? 'Timers need the connection to the soundbar.' : running ? running + (running > 1 ? ' timers' : ' timer') + ' running. They also work with the bar in standby.' : 'The text shows on the bar when the time is up, until you press any key on the JBL remote (10 minutes at most).');
    renderList();
  }
  const refresh = () => { problem = ''; if (latest) render(latest); };
  text.addEventListener('input', refresh);
  minutes.addEventListener('input', refresh);
  countdown.addEventListener('change', refresh);
  for (const preset of root.querySelectorAll('[data-minutes]')) preset.addEventListener('click', () => { minutes.value = preset.dataset.minutes; refresh(); });
  start.addEventListener('click', () => {
    const d = draft();
    if (start.disabled || d.error) return;
    starting = d.parameters;
    request('timer_start');
  });
  list.addEventListener('click', event => {
    const button = event.target.closest('[data-timer]');
    if (!button || button.disabled) return;
    cancelling = button.dataset.timer;
    request('timer_cancel');
  });
  // The time left counts down here between state updates.
  setInterval(() => { if (latest && timers().some(t => !t.ringing)) renderList(); }, 1000);
  return {
    render,
    parameters(action) {
      if (action === 'timer_start') return starting;
      if (action === 'timer_cancel') return cancelling ? {id: cancelling} : null;
      return null;
    },
    started() { text.value = ''; },
  };
}
