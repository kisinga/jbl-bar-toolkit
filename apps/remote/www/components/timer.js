import {byId} from './dom.js';
// Timers on the front display (extension bar91.timer). They work with the bar
// on or in standby; when one ends, TIME'S UP and its name scroll until a key of
// the bar's remote is pressed, it is stopped here, or 10 minutes pass.
// Layout: running timers first, then recent ones to start again in one tap,
// then a new timer (minutes, name, folded options, Start, and what will
// happen, folded). After a start, a toast offers Undo.
const MAX_MINUTES = 720;
const MAX_TIMERS = 8;
const RECENT_MAX = 4;
const TOAST_MS = 8000;
const OPTIONS_KEY = 'jbl-timer-options';
const RECENT_KEY = 'jbl-timer-recent';
const DEFAULTS = {countdown: false, sound: 'unless_tv', volume: 10, ring: 30};
// Like the remote: upper case, no accents; letters, digits, space and - . ' (the
// characters the display shows correctly)
const fold = text => text.normalize('NFD').replace(/[̀-ͯ]/g, '').replace(/œ/gi, 'OE').replace(/æ/gi, 'AE').replace(/ß/g, 'SS').replace(/[’‘]/g, "'").replace(/[–—]/g, '-').toUpperCase().trim().split(/\s+/).join(' ');
const validText = text => text === '' || /^[A-Z0-9 .'-]{1,31}$/.test(text);
// The bar's own spelling (4'59), used in what will happen.
export const timerClock = ms => {
  const s = Math.max(0, Math.ceil(ms / 1000));
  const two = n => String(n).padStart(2, '0');
  return s >= 3600 ? Math.floor(s / 3600) + 'H' + two(Math.floor(s / 60) % 60) + "'" + two(s % 60) : Math.floor(s / 60) + "'" + two(s % 60);
};
// The page's spelling: 4:59, 1:04:59.
export const pageClock = ms => {
  const s = Math.max(0, Math.ceil(ms / 1000));
  const two = n => String(n).padStart(2, '0');
  return s >= 3600 ? Math.floor(s / 3600) + ':' + two(Math.floor(s / 60) % 60) + ':' + two(s % 60) : Math.floor(s / 60) + ':' + two(s % 60);
};
const clockTime = date => date.toLocaleTimeString([], {hour: '2-digit', minute: '2-digit'});
const minutesLabel = m => m % 60 === 0 && m >= 60 ? (m / 60) + (m === 60 ? ' hour' : ' hours') : m + ' min';
const title = text => text ? text.charAt(0) + text.slice(1).toLowerCase() : 'Timer';
const ringLabel = s => s >= 60 ? (s / 60) + ' min' : s + ' s';
// Browser storage is a convenience: private windows may refuse it.
const load = (key, fallback) => { try { const v = JSON.parse(localStorage.getItem(key)); return v ?? fallback; } catch { return fallback; } };
const save = (key, value) => { try { localStorage.setItem(key, JSON.stringify(value)); } catch {} };
const SVG = 'http://www.w3.org/2000/svg';

export function createTimer(root, {request, supports = () => true}) {
  const $ = id => byId(root, id);
  const doc = root.ownerDocument;
  const text = $('timer-text'), minutes = $('timer-minutes'), countdown = $('timer-countdown'), volume = $('timer-volume'), start = $('timer-start'), list = $('timer-list');
  const soundGroup = $('timer-sound'), ringGroup = $('timer-ring'), toast = $('timer-toast');
  const stored = load(OPTIONS_KEY, {});
  const options = {...DEFAULTS, ...(stored && typeof stored === 'object' ? stored : {})};
  if (!['off', 'unless_tv', 'always'].includes(options.sound)) options.sound = DEFAULTS.sound;
  if (![10, 30, 60, 120].includes(options.ring)) options.ring = DEFAULTS.ring;
  if (!Number.isInteger(options.volume) || options.volume < 1 || options.volume > 30) options.volume = DEFAULTS.volume;
  countdown.checked = !!options.countdown;
  volume.value = options.volume;
  let recent = load(RECENT_KEY, []);
  if (!Array.isArray(recent)) recent = [];
  recent = recent.filter(r => r && validText(r.text ?? '') && Number.isInteger(r.minutes) && r.minutes >= 1 && r.minutes <= MAX_MINUTES).slice(0, RECENT_MAX);
  let latest = null, seen = '', received = 0, problem = '', menu = null, toastTimer = null, undo = null, fresh = null;
  const calm = () => root.ownerDocument.defaultView?.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  const outbox = {timer_start: [], timer_cancel: [], timer_extend: []};
  const timers = () => latest?.state?.timers || [];
  const busy = () => !latest?.ready || !!latest?.active;
  const remaining = t => t.remaining_ms - (Date.now() - received);

  function send(action, parameters) {
    outbox[action].push(parameters);
    if (request(action) === false) outbox[action].pop();
  }
  function settings() {
    return {countdown: countdown.checked, sound: options.sound, volume: Number(volume.value), ring: options.ring};
  }
  function check(name, m, o) {
    if (!validText(name)) return {error: "Name: letters, digits, space and - . ' only (31 at most).", field: true};
    if (!Number.isInteger(m) || m < 1 || m > MAX_MINUTES) return {error: 'Minutes: 1 to 720.'};
    if (o.sound !== 'off' && (!Number.isInteger(o.volume) || o.volume < 1 || o.volume > 30)) return {error: 'Volume: 1 to 30.'};
    if (timers().some(t => t.text === name)) return name ? {error: title(name) + ' is already running. Choose another name, like ' + title(name) + ' 2.', field: true} : {error: 'Give this one a name: a timer without a name is already running.', field: true, hint: true};
    if (timers().length >= MAX_TIMERS) return {error: 'At most 8 timers at a time.'};
    return {parameters: {...(name ? {text: name} : {}), duration_s: m * 60, countdown: o.countdown, sound: o.sound, ...(o.sound !== 'off' ? {sound_volume: o.volume, ring_s: o.ring} : {})}};
  }
  const draft = () => check(fold(text.value), Number(minutes.value), settings());

  function launch(parameters, label) {
    send('timer_start', parameters);
    undo = {text: parameters.text ?? '', label, id: null, at: Date.now() + parameters.duration_s * 1000, sound: parameters.sound};
  }
  function remember(p) {
    const entry = {text: p.text ?? '', minutes: p.duration_s / 60, countdown: p.countdown, sound: p.sound, volume: p.sound_volume ?? options.volume, ring: p.ring_s ?? options.ring};
    recent = [entry, ...recent.filter(r => r.text !== entry.text || r.minutes !== entry.minutes)].slice(0, RECENT_MAX);
    save(RECENT_KEY, recent);
  }

  function svg(tag, attrs) {
    const e = doc.createElementNS(SVG, tag);
    for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
    return e;
  }
  function ring(fraction) {
    const s = svg('svg', {viewBox: '0 0 44 44', class: 'timer-ring', 'aria-hidden': 'true'});
    const c = 2 * Math.PI * 18;
    s.append(svg('circle', {cx: 22, cy: 22, r: 18, class: 'timer-ring-track'}),
      svg('circle', {cx: 22, cy: 22, r: 18, class: 'timer-ring-left', 'stroke-dasharray': c.toFixed(1), 'stroke-dashoffset': (c * (1 - Math.min(1, Math.max(0, fraction)))).toFixed(1), transform: 'rotate(-90 22 22)'}));
    return s;
  }
  function button(label, className, onClick, aria) {
    const b = doc.createElement('button');
    b.type = 'button'; b.className = className; b.textContent = label;
    if (aria) b.setAttribute('aria-label', aria);
    b.disabled = busy();
    b.addEventListener('click', onClick);
    return b;
  }
  function el(tag, className, content) {
    const e = doc.createElement(tag);
    if (className) e.className = className;
    if (content !== undefined) e.textContent = content;
    return e;
  }

  function renderList() {
    const items = timers();
    const extend = supports('timer_extend');
    list.replaceChildren(...items.map(t => {
      const name = title(t.text);
      const item = el('li', 'timer-item' + (t.ringing ? ' ringing' : ''));
      item.dataset.timer = t.id;
      if (t.ringing) {
        const head = el('div', 'timer-ringing-head');
        head.append(el('span', 'timer-name', name), el('span', 'timer-detail', "Time's up · press any key on the JBL remote"));
        item.append(head, button('Stop', 'timer-stop', () => send('timer_cancel', {id: t.id}), 'Stop ' + name));
        return item;
      }
      const left = remaining(t);
      const total = t.duration_ms || Math.max(left, t.remaining_ms);
      const words = el('span', 'timer-words');
      const extras = [t.countdown ? 'on the bar' : '', t.sound === 'always' ? 'chime' : t.sound === 'unless_tv' ? 'chime unless TV' : 'silent'].filter(Boolean);
      words.append(el('span', 'timer-name', name), el('span', 'timer-detail', 'Ends at ' + clockTime(new Date(Date.now() + left)) + ' · ' + extras.join(' · ')));
      const more = button('', 'timer-more', () => { menu = menu === t.id ? null : t.id; renderList(); }, 'More for ' + name);
      more.setAttribute('aria-expanded', String(menu === t.id));
      more.append(svg('svg', {viewBox: '0 0 24 24', 'aria-hidden': 'true'}));
      for (const cy of [5, 12, 19]) more.lastChild.append(svg('circle', {cx: 12, cy, r: 1.6, fill: 'currentColor', stroke: 'none'}));
      more.disabled = false;
      // The time left, and +1 min at hand under it (a quick tap while cooking).
      const time = el('span', 'timer-time');
      time.append(el('span', 'timer-left', pageClock(left)));
      if (extend) time.append(button('+1 min', 'timer-add timer-add-one', () => send('timer_extend', {id: t.id, add_s: 60}), 'Add 1 minute to ' + name));
      if (fresh?.id === t.id && Date.now() < fresh.until) item.classList.add('just-started');
      item.append(ring(left / total), words, time, more);
      if (menu === t.id) {
        const actions = el('div', 'timer-actions');
        if (extend) actions.append(button('+5 min', 'timer-add', () => send('timer_extend', {id: t.id, add_s: 300}), 'Add 5 minutes to ' + name));
        actions.append(button('Cancel', 'timer-cancel', () => { menu = null; send('timer_cancel', {id: t.id}); }, 'Cancel ' + name));
        item.append(actions);
      }
      return item;
    }));
    list.hidden = items.length === 0;
  }

  function renderRecent() {
    const box = $('timer-recent'), holder = $('timer-recent-list');
    box.hidden = recent.length === 0;
    holder.replaceChildren(...recent.map(r => {
      const p = check(r.text, r.minutes, {countdown: r.countdown, sound: r.sound, volume: r.volume, ring: r.ring});
      const b = el('button', 'timer-recent-item');
      b.type = 'button';
      b.disabled = busy() || !!p.error;
      b.title = p.error || '';
      const play = svg('svg', {viewBox: '0 0 24 24', 'aria-hidden': 'true'});
      play.append(svg('path', {d: 'M7 5v14l12-7z'}));
      const label = el('span');
      if (r.text) label.append(el('strong', '', title(r.text)), ' · ');
      label.append(minutesLabel(r.minutes));
      b.append(play, label);
      b.setAttribute('aria-label', 'Start again: ' + (r.text ? title(r.text) + ', ' : '') + minutesLabel(r.minutes));
      b.addEventListener('click', () => { if (!p.error && !busy()) launch(p.parameters, r.text ? title(r.text) : minutesLabel(r.minutes) + ' timer'); });
      return b;
    }));
  }

  function soundSentence(o) {
    if (o.sound === 'off') return 'No sound.';
    return 'A chime rings for ' + ringLabel(o.ring) + ' at volume ' + o.volume + ' or more' + (o.sound === 'always' ? ', even during a film.' : ", unless you're watching TV.");
  }
  function renderExplain(name, m, o) {
    const end = new Date(Date.now() + m * 60000);
    const others = timers().some(t => !t.ringing);
    $('timer-explain-summary').textContent = (o.sound === 'off' ? 'Ends at ' : 'Rings at ') + clockTime(end) + ' · what will happen?';
    $('timer-explain-now').textContent = !o.countdown ? 'Nothing changes on the bar. The timer runs even in standby.' : others ? 'With other timers running, the bar shows each time left once a minute.' : 'The bar shows the time left: ' + timerClock(m * 60000) + ', ' + timerClock(m * 60000 - 1000) + '…';
    $('timer-explain-at').textContent = clockTime(end);
    $('timer-explain-end').textContent = (name ? "TIME'S UP " + name : "TIME'S UP - PRESS ANY KEY") + ' scrolls on the bar. ' + soundSentence(o);
    $('timer-explain-stop').textContent = 'Any key on the JBL remote, or Stop here. ' + (o.sound !== 'off' ? 'Volume, source and standby then go back as they were. ' : '') + 'After 10 minutes it stops by itself.';
  }
  function renderOptions(o) {
    for (const b of soundGroup.querySelectorAll('[data-sound]')) { const on = b.dataset.sound === o.sound; b.setAttribute('aria-checked', String(on)); b.classList.toggle('selected', on); b.tabIndex = on ? 0 : -1; }
    for (const b of ringGroup.querySelectorAll('[data-ring]')) { const on = Number(b.dataset.ring) === o.ring; b.setAttribute('aria-checked', String(on)); b.classList.toggle('selected', on); b.tabIndex = on ? 0 : -1; }
    $('timer-sound-hint').textContent = o.sound === 'off' ? 'Only the text on the bar.' : o.sound === 'always' ? 'Rings even during a film.' : 'Silent while the bar plays TV or HDMI, so a film is never cut.';
    $('timer-sound-options').hidden = o.sound === 'off';
    const chime = o.sound === 'off' ? 'No chime' : (o.sound === 'always' ? 'Chime always' : 'Chime unless watching TV') + (o.volume !== DEFAULTS.volume ? ' · volume ' + o.volume + '+' : '') + (o.ring !== DEFAULTS.ring ? ' · ' + ringLabel(o.ring) : '');
    $('timer-options-summary').textContent = chime + ' · ' + (o.countdown ? 'countdown on the bar' : 'no countdown');
  }
  function renderToast() {
    if (!undo) { toast.hidden = true; return; }
    if (!undo.id) {
      const t = timers().find(t => t.text === undo.text && !t.ringing);
      if (t) undo.id = t.id;
    }
    if (undo.id && undo.shown && !undo.seen) {
      // Show the timer just started, where it runs: the form below is
      // already ready for the next one.
      undo.seen = true;
      fresh = {id: undo.id, until: Date.now() + 2500};
      renderList();
      list.querySelector('[data-timer="' + undo.id + '"]')?.scrollIntoView?.({block: 'center', behavior: calm() ? 'auto' : 'smooth'});
    }
    toast.hidden = !undo.id || !undo.shown;
    $('timer-toast-text').textContent = undo.label + ' started. ' + (undo.sound === 'off' ? 'Ends' : 'Rings') + ' at ' + clockTime(new Date(undo.at)) + '.';
    $('timer-undo').disabled = busy();
  }

  function render(context) {
    // remaining_ms is as of the last state from the soundbar: count from its arrival.
    const key = JSON.stringify(context.state?.timers || []);
    if (key !== seen) { seen = key; received = Date.now(); }
    latest = context;
    // Parameters wait for their action in the shared queue; a cleared queue
    // (disconnection, an error) drops theirs too.
    for (const [action, pending] of Object.entries(outbox)) {
      const queued = (context.pending || []).filter(a => a === action).length - (context.active === action ? 1 : 0);
      pending.splice(0, Math.max(0, pending.length - queued));
    }
    if (menu &&!timers().some(t => t.id === menu && !t.ringing)) menu = null;
    if (undo?.id && !timers().some(t => t.id === undo.id)) hideToast();
    const name = fold(text.value), m = Number(minutes.value), o = settings();
    const d = check(name, m, o);
    for (const b of root.querySelectorAll('[data-minutes]')) b.setAttribute('aria-pressed', String(Number(b.dataset.minutes) === m));
    renderOptions(o);
    renderExplain(name, Number.isInteger(m) && m > 0 ? m : 1, o);
    start.disabled = busy() || !!d.error;
    start.textContent = context.active === 'timer_start' ? 'Starting…' : 'Start ' + (Number.isInteger(m) && m > 0 ? minutesLabel(m).replace(' min', '-minute').replace(' hours', '-hour').replace(' hour', '-hour') : '') + ' timer';
    const fieldError = d.error && d.field ? d.error : '';
    $('timer-text-error').hidden = !fieldError;
    $('timer-text-error').textContent = fieldError;
    // Not a mistake: a second timer without a name only needs one.
    $('timer-text-error').classList.toggle('timer-hint', !!d.hint);
    text.setAttribute('aria-invalid', String(!!fieldError && !d.hint));
    $('timer-state').textContent = problem || (d.error && !d.field ? d.error : !context.ready ? 'Timers need the connection to the soundbar.' : '');
    $('timer-state').hidden = !$('timer-state').textContent;
    renderList();
    renderRecent();
    renderToast();
  }
  const refresh = () => { problem = ''; if (latest) render(latest); };
  const keep = () => { options.countdown = countdown.checked; options.volume = Number(volume.value) || options.volume; save(OPTIONS_KEY, options); };
  function hideToast() { clearTimeout(toastTimer); toastTimer = null; undo = null; toast.hidden = true; }

  text.addEventListener('input', refresh);
  minutes.addEventListener('input', refresh);
  const step = delta => { const m = Number(minutes.value); minutes.value = Math.min(MAX_MINUTES, Math.max(1, (Number.isInteger(m) ? m : 5) + delta)); refresh(); };
  $('timer-less').addEventListener('click', () => step(-1));
  $('timer-more').addEventListener('click', () => step(1));
  for (const preset of root.querySelectorAll('[data-minutes]')) preset.addEventListener('click', () => { minutes.value = preset.dataset.minutes; refresh(); });
  countdown.addEventListener('change', () => { keep(); refresh(); });
  volume.addEventListener('input', () => { if (Number(volume.value) >= 1 && Number(volume.value) <= 30) keep(); refresh(); });
  for (const b of root.querySelectorAll('[data-volume]')) b.addEventListener('click', () => { volume.value = Math.min(30, Math.max(1, (Number(volume.value) || options.volume) + Number(b.dataset.volume))); keep(); refresh(); });
  // Radio groups: click, or arrows to move the choice.
  function radios(group, attr, set) {
    const all = [...group.querySelectorAll('[' + attr + ']')];
    for (const b of all) b.addEventListener('click', () => { set(b); keep(); refresh(); });
    group.addEventListener('keydown', event => {
      const delta = {ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1}[event.key];
      if (!delta) return;
      event.preventDefault();
      const i = all.indexOf(doc.activeElement);
      const next = all[(Math.max(0, i) + delta + all.length) % all.length];
      set(next); keep(); refresh(); next.focus();
    });
  }
  radios(soundGroup, 'data-sound', b => { options.sound = b.dataset.sound; });
  radios(ringGroup, 'data-ring', b => { options.ring = Number(b.dataset.ring); });
  start.addEventListener('click', () => {
    const d = draft();
    if (start.disabled || d.error) return;
    launch(d.parameters, d.parameters.text ? title(d.parameters.text) : minutesLabel(Number(minutes.value)) + ' timer');
  });
  $('timer-undo').addEventListener('click', () => {
    if (!undo?.id || busy()) return;
    send('timer_cancel', {id: undo.id});
    hideToast();
  });
  // The time left counts down here between state updates.
  setInterval(() => { if (latest && timers().some(t => !t.ringing)) renderList(); }, 1000);
  setInterval(() => { if (latest) render(latest); }, 15000); // end times in what will happen
  return {
    render,
    parameters(action) {
      return outbox[action]?.shift() ?? null;
    },
    // A start the soundbar confirmed: remember it, offer Undo, clear the name.
    started(parameters) {
      if (parameters) remember(parameters);
      if (undo) {
        undo.shown = true;
        clearTimeout(toastTimer);
        toastTimer = setTimeout(() => { hideToast(); }, TOAST_MS);
      }
      text.value = '';
      refresh();
    },
    // A refused action drops the parameters it was sent with.
    failed(action) {
      for (const pending of Object.values(outbox)) pending.length = 0; // the queue is cleared too
      if (action === 'timer_start') hideToast();
    },
  };
}
