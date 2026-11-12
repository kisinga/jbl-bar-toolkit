import {byId} from './dom.js';
// Messages on the front display (extension bar91.display-message): the ones in
// course, each with ×, Clear the unnamed one and Clear all; recent messages to
// send again; and a form to leave one (text, optional name, how long).
const RECENT_KEY = 'jbl-message-recent';
const RECENT_MAX = 4;
// Like the remote (display_message.go): upper case, accents folded, letters,
// digits, space and - . ' only, 31 at most.
const fold = text => text.normalize('NFD').replace(/[̀-ͯ]/g, '').replace(/œ/gi, 'OE').replace(/æ/gi, 'AE').replace(/ß/g, 'SS').replace(/[’‘]/g, "'").replace(/[–—]/g, '-').toUpperCase().trim().split(/\s+/).join(' ');
const validText = text => /^[A-Z0-9 .'-]{1,31}$/.test(text);
const validId = id => /^[a-z0-9_-]{1,32}$/.test(id);
const load = (key, fallback) => { try { const v = JSON.parse(localStorage.getItem(key)); return v ?? fallback; } catch { return fallback; } };
const save = (key, value) => { try { localStorage.setItem(key, JSON.stringify(value)); } catch {} };
const clock = ms => { const s = Math.max(0, Math.ceil(ms / 1000)); return s >= 3600 ? Math.floor(s / 3600) + ':' + String(Math.floor(s / 60) % 60).padStart(2, '0') + ':' + String(s % 60).padStart(2, '0') : Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0'); };
const durationLabel = s => s >= 3600 ? (s / 3600) + (s === 3600 ? ' hour' : ' hours') : (s / 60) + ' min';
const times = n => n === 1 ? 'once' : n === 2 ? 'twice' : n + ' times';
// How long, in words, for the summary and the recent messages.
export function howLong(o) {
  return o.mode === 'forever' ? 'until cleared' : o.mode === 'duration' ? 'for ' + durationLabel(o.duration) : times(o.passes);
}

export function createMessages(root, {request}) {
  const $ = id => byId(root, id);
  const doc = root.ownerDocument;
  const keyClears = $('message-stop-on-key');
  const text = $('message-input'), name = $('message-id'), passes = $('message-passes'), send = $('message-send'), list = $('message-list');
  const modeGroup = $('message-mode'), durationGroup = $('message-duration');
  const o = {mode: 'forever', passes: 1, duration: 300}; // left for someone: until it is seen
  let latest = null, received = 0, seen = '', confirmAll = null, confirmTimer = null;
  let recent = load(RECENT_KEY, []);
  if (!Array.isArray(recent)) recent = [];
  recent = recent.filter(r => r && validText(r.text ?? '') && (r.id === '' || validId(r.id ?? ''))).slice(0, RECENT_MAX);
  const outbox = {display_message: [], display_cancel: [], display_clock: []};
  const clockSwitch = $('message-clock');
  const messages = () => latest?.state?.messages || [];
  const busy = () => !latest?.ready || !!latest?.active;

  function send_(action, parameters) {
    outbox[action].push(parameters);
    if (request(action) === false) outbox[action].pop();
  }
  function parameters(t, id, opt) {
    const p = {text: t};
    if (id) p.id = id;
    if (opt.mode === 'forever') p.forever = true;
    else if (opt.mode === 'duration') p.duration_s = opt.duration;
    else p.repeat = opt.passes;
    if (opt.seen) p.stop_on_key = true;
    return p;
  }
  function draft() {
    const shown = fold(text.value), id = name.value.trim().toLowerCase();
    if (!text.value.trim()) return {empty: true};
    if (!validText(shown)) return {error: shown.length > 31 ? 'At most 31 characters: the bar shows ' + shown.length + '.' : "Letters, digits, space and - . ' only (the display cannot show the others).", field: 'text'};
    if (id && !validId(id)) return {error: 'Name: lower-case letters, digits, - and _ only (32 at most).', field: 'id'};
    if (o.mode === 'passes' && !(Number.isInteger(o.passes) && o.passes >= 1 && o.passes <= 99)) return {error: 'Times: 1 to 99.'};
    return {shown, id, parameters: parameters(text.value.trim(), id, {...o, seen: keyClears.checked})};
  }

  function el(tag, className, content) {
    const e = doc.createElement(tag);
    if (className) e.className = className;
    if (content !== undefined) e.textContent = content;
    return e;
  }
  function button(label, className, onClick, aria) {
    const b = el('button', className, label);
    b.type = 'button';
    if (aria) b.setAttribute('aria-label', aria);
    b.disabled = busy();
    b.addEventListener('click', onClick);
    return b;
  }
  function renderList() {
    const items = messages();
    $('messages-current').hidden = items.length === 0;
    list.replaceChildren(...items.map(m => {
      const item = el('li', 'message-item' + (m.showing ? ' showing' : ''));
      item.dataset.message = m.id;
      const words = el('span', 'message-words');
      const left = m.mode === 'forever' ? 'until cleared' : m.mode === 'duration' ? clock(m.remaining_ms - (Date.now() - received)) + ' left' : m.passes_left === 1 ? 'last time' : m.passes_left + ' times left';
      words.append(el('span', 'message-text', m.text), el('span', 'message-detail', (m.named ? m.id : 'Unnamed') + ' · ' + left + (m.stop_on_key ? ' or a key' : '') + ' · ' + (m.showing ? 'on the bar now' : 'waiting')));
      const cancel = button('', 'timer-cancel', () => send_('display_cancel', {id: m.id}), 'Clear ' + (m.named ? m.id : 'the unnamed message'));
      const icon = doc.createElementNS('http://www.w3.org/2000/svg', 'svg');
      icon.setAttribute('viewBox', '0 0 24 24'); icon.setAttribute('aria-hidden', 'true');
      const path = doc.createElementNS('http://www.w3.org/2000/svg', 'path');
      path.setAttribute('d', 'M6 6l12 12M18 6 6 18');
      icon.append(path); cancel.append(icon);
      item.append(el('span', 'message-dot'), words, cancel);
      return item;
    }));
    const all = $('message-clear-all');
    all.disabled = busy();
    all.textContent = confirmAll ? 'Tap again to clear ' + (items.length === 1 ? 'it' : 'all ' + items.length) : items.length === 1 ? 'Clear it' : 'Clear all messages';
    all.classList.toggle('confirm', !!confirmAll);
  }
  function renderRecent() {
    $('message-recent').hidden = recent.length === 0;
    $('message-recent-list').replaceChildren(...recent.map(r => {
      const b = el('button', 'timer-recent-item');
      b.type = 'button';
      b.disabled = busy();
      const play = doc.createElementNS('http://www.w3.org/2000/svg', 'svg');
      play.setAttribute('viewBox', '0 0 24 24'); play.setAttribute('aria-hidden', 'true');
      const path = doc.createElementNS('http://www.w3.org/2000/svg', 'path');
      path.setAttribute('d', 'M7 5v14l12-7z');
      play.append(path);
      const label = el('span');
      label.append(el('strong', '', r.text), ' · ' + howLong(r));
      b.append(play, label);
      b.setAttribute('aria-label', 'Send again: ' + r.text + ', ' + howLong(r));
      b.addEventListener('click', () => { if (!busy()) send_('display_message', parameters(r.text, r.id, r)); });
      return b;
    }));
  }
  function renderOptions() {
    for (const b of modeGroup.querySelectorAll('[data-mode]')) { const on = b.dataset.mode === o.mode; b.setAttribute('aria-checked', String(on)); b.classList.toggle('selected', on); b.tabIndex = on ? 0 : -1; }
    for (const b of durationGroup.querySelectorAll('[data-duration]')) { const on = Number(b.dataset.duration) === o.duration; b.setAttribute('aria-checked', String(on)); b.classList.toggle('selected', on); b.tabIndex = on ? 0 : -1; }
    $('message-passes-row').hidden = o.mode !== 'passes';
    $('message-duration-row').hidden = o.mode !== 'duration';
    const summary = howLong(o);
    $('message-options-summary').textContent = summary.charAt(0).toUpperCase() + summary.slice(1);
  }
  function render(context) {
    const key = JSON.stringify(context.state?.messages || []);
    if (key !== seen) { seen = key; received = Date.now(); }
    latest = context;
    // Parameters wait for their action in the shared queue; a cleared queue drops theirs too.
    for (const [action, pending] of Object.entries(outbox)) {
      const queued = (context.pending || []).filter(a => a === action).length - (context.active === action ? 1 : 0);
      pending.splice(0, Math.max(0, pending.length - queued));
    }
    const d = draft();
    renderOptions();
    $('message-preview').textContent = d.empty ? "Letters, digits, space and - . ' (31 at most). The bar shows capitals." : d.field === 'text' ? d.error : 'The bar shows: ' + d.shown;
    $('message-preview').classList.toggle('timer-error', d.field === 'text');
    text.setAttribute('aria-invalid', String(d.field === 'text'));
    name.setAttribute('aria-invalid', String(d.field === 'id'));
    send.disabled = busy() || !!d.error || !!d.empty;
    send.textContent = context.active === 'display_message' ? 'Sending…' : 'Show on the bar';
    const replaces = d.id ? messages().some(m => m.id === d.id) : messages().some(m => !m.named);
    $('message-explain').textContent = d.empty || d.error ? '' : 'Shows ' + howLong(o) + (keyClears.checked ? ', or until someone presses a key on the JBL remote' : '') + ', after the bar\'s own messages and in turn with the others.' + (replaces ? ' It replaces ' + (d.id ? 'the message named ' + d.id : 'the unnamed message') + '.' : '');
    $('message-state').textContent = d.error && d.field !== 'text' ? d.error : !context.ready ? 'Messages need the connection to the soundbar.' : '';
    renderList();
    renderRecent();
    renderClock();
  }
  // The standby clock (bar91.display-clock): the switch follows the bar unless a change is on its way.
  function renderClock() {
    const c = latest?.state?.display_clock;
    if (!c) return;
    if (!outbox.display_clock.length && latest.active !== 'display_clock') clockSwitch.checked = !!c.enabled;
    clockSwitch.disabled = busy();
  }
  const refresh = () => { if (latest) render(latest); };
  text.addEventListener('input', refresh);
  keyClears.addEventListener('change', refresh);
  name.addEventListener('input', refresh);
  passes.addEventListener('input', () => { o.passes = Number(passes.value); refresh(); });
  for (const b of root.querySelectorAll('[data-passes]')) b.addEventListener('click', () => { o.passes = Math.min(99, Math.max(1, (Number(passes.value) || 1) + Number(b.dataset.passes))); passes.value = o.passes; refresh(); });
  function radios(group, attr, set) {
    const all = [...group.querySelectorAll('[' + attr + ']')];
    for (const b of all) b.addEventListener('click', () => { set(b); refresh(); });
    group.addEventListener('keydown', event => {
      const delta = {ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1}[event.key];
      if (!delta) return;
      event.preventDefault();
      const next = all[(Math.max(0, all.indexOf(doc.activeElement)) + delta + all.length) % all.length];
      set(next); refresh(); next.focus();
    });
  }
  radios(modeGroup, 'data-mode', b => { o.mode = b.dataset.mode; });
  radios(durationGroup, 'data-duration', b => { o.duration = Number(b.dataset.duration); });
  clockSwitch.addEventListener('change', () => {
    if (busy()) { clockSwitch.checked = !clockSwitch.checked; return; }
    let tz = '';
    try { tz = Intl.DateTimeFormat().resolvedOptions().timeZone || ''; } catch { tz = ''; }
    send_('display_clock', tz ? {enabled: clockSwitch.checked, tz} : {enabled: clockSwitch.checked});
  });
  send.addEventListener('click', () => { const d = draft(); if (!send.disabled && d.parameters) send_('display_message', d.parameters); });
  // Clear all asks for a second tap, rather than a dialog.
  $('message-clear-all').addEventListener('click', () => {
    if (busy()) return;
    if (!confirmAll) {
      confirmAll = true;
      clearTimeout(confirmTimer);
      confirmTimer = setTimeout(() => { confirmAll = null; refresh(); }, 4000);
      refresh();
      return;
    }
    confirmAll = null;
    clearTimeout(confirmTimer);
    send_('display_cancel', null);
  });
  setInterval(() => { if (latest && messages().some(m => m.mode === 'duration')) renderList(); }, 1000);
  return {
    render,
    parameters(action) { return outbox[action]?.length ? outbox[action].shift() : null; },
    // A message the soundbar took: remember it to send again, clear the form.
    sent(p) {
      if (!p) return;
      const entry = {text: fold(p.text), id: p.id ?? '', mode: p.forever ? 'forever' : p.duration_s ? 'duration' : 'passes', passes: p.repeat ?? 1, duration: p.duration_s ?? 300, seen: !!p.stop_on_key};
      recent = [entry, ...recent.filter(r => r.text !== entry.text || r.id !== entry.id)].slice(0, RECENT_MAX);
      save(RECENT_KEY, recent);
      if (fold(text.value) === entry.text) text.value = '';
      refresh();
    },
    failed() { for (const pending of Object.values(outbox)) pending.length = 0; },
  };
}
