import {createEqualizer} from './components/equalizer.js';
import {createSystem} from './components/system.js';
import {createPlayback} from './components/playback.js';
import {createSound} from './components/sound.js';
import {createBluetooth} from './components/bluetooth.js';
import {createDetails} from './components/details.js';
import {createCapabilities} from './components/capabilities.js';
import {createBassOffset} from './components/bass-offset.js';
import {createTimer} from './components/timer.js';
import {createMessages} from './components/messages.js';
import {createRestart} from './components/restart.js';
import {createAdvanced} from './components/advanced.js';
import {createSpeakers} from './components/speakers.js';

const $ = id => document.getElementById(id);
const buttons = [...document.querySelectorAll('[data-action]')];
const isVolume = action => action === 'volume_up' || action === 'volume_down';
const delta = action => action === 'volume_up' ? 1 : action === 'volume_down' ? -1 : 0;
let current, csrf = '', online = false, active = null, queue = [], epoch = 0;
let refreshing = false, stream = null, latestState = null, lastSync = 0, holdTimer, repeatTimer, heldButton;
const equalizer=createEqualizer($('eq-panel'));
const system=createSystem($('system-panel'));
const playback=createPlayback($('playback'));
const sound=createSound(document.querySelector('.mode-panel'));
const bluetooth=createBluetooth($('bluetooth-panel'));
const details=createDetails(document.querySelector('.more-details'));
const capabilities=createCapabilities(document);
const bassOffset=createBassOffset($('bass-offset-panel'));
// Timers have their own buttons: they work in standby, unlike the bar controls.
const timer=createTimer($('timer-panel'),{supports:action=>capabilities.command(action).action.includes('.'),request:action=>{if(!csrf||!online||queue.length>=8)return false;queue.push(action);if(active){render();feedback(pendingMessage(),'pending');}else void pump();return true;}});
const advanced=createAdvanced($('advanced'),$('room-setup'));
const speakers=createSpeakers($('speaker-view'));
const messages=createMessages($('messages-panel'),{request:action=>{if(!csrf||!online||queue.length>=8)return false;queue.push(action);if(active){render();feedback(pendingMessage(),'pending');}else void pump();return true;}});
const restart=createRestart($('restart-panel'),{request:action=>{if(!csrf||!online||queue.length>=8)return false;queue.push(action);if(active){render();feedback(pendingMessage(),'pending');}else void pump();return true;}});
const components=[bluetooth,sound,playback,system,equalizer,details,advanced,bassOffset,speakers,timer,messages,restart];
let syncTimer=null;
// Readiness shared by the shell and every component. A control sent from
// elsewhere (another page, Home Assistant, the wake automation) holds the bar:
// wait for it like for one of ours. Background work that ends within
// BUSY_DELAY (a monitor's quick cleanup) is not shown, so the page does not
// flash "Another control is running" and disable every control for a moment.
const BUSY_DELAY = 800;
let busySince = null, busyTimer = null;
function readiness() {
  const reachable = online && !!current?.available;
  const busy = reachable && !!current?.control?.busy && active === null;
  if (!busy) {
    busySince = null;
    if (busyTimer) { clearTimeout(busyTimer); busyTimer = null; }
  } else if (busySince === null) {
    busySince = performance.now();
    busyTimer = setTimeout(() => { busyTimer = null; render(); }, BUSY_DELAY);
  }
  const elsewhere = busy && performance.now() - busySince >= BUSY_DELAY;
  return {reachable, elsewhere, ready: reachable && !elsewhere};
}
const context=()=>{
  const status=readiness();
  return {state:capabilities.state(current),deviceName:capabilities.name(),online,reachable:status.reachable,ready:status.ready,busy:status.elsewhere ? current.control : null,awake:current?.power==='On',active,queued:queue.length,pending:active?[active,...queue]:[...queue],csrf};
};
function feedback(text, kind = '') {
  $('message-text').textContent = text;
  $('message').dataset.kind = kind;
}
function sourceName(s) { return /^Input \d+$/.test(s.input) ? 'Other source' : s.input || 'Unknown source'; }
function settingsChanged(s) { return current && (['volume','muted','power','input','mode'].some(key=>current[key]!==s[key]) || JSON.stringify(current.sound)!==JSON.stringify(s.sound)); }
function pendingVolume() { return (active ? delta(active) : 0) + queue.reduce((n, action) => n + delta(action), 0); }
function render() {
  const s = current;
  if (!s) return;
  const {reachable, elsewhere, ready} = readiness();
  const awake = s.power === 'On';
  const standby = s.power === 'Standby';
  const knownPower = awake || standby;
  const pending = active !== null || queue.length > 0 || elsewhere;
  $('connection').dataset.state = reachable ? 'online' : online ? 'connecting' : 'offline';
  $('connection-label').textContent = reachable ? 'Connected' : online ? 'Starting' : 'Offline';
  $('summary').textContent = !online ? 'Connection lost. Showing the last known settings.' : !s.available ? (s.error || 'Waiting for your soundbar…') : elsewhere ? 'Another control is running; controls return when it finishes.' : awake ? sourceName(s) + (['Smart','Standard'].includes(s.mode) ? ' · ' + s.mode + ' mode' : '') : standby ? 'In standby. Ready when you are.' : s.power === 'Starting' ? 'Your soundbar is starting…' : 'Connected. Power state is not reported.';
  const offset = pendingVolume();
  $('volume').textContent = s.volume == null ? '—' : Math.max(0, s.volume + offset);
  $('volume-caption').textContent = standby ? 'IN STANDBY' : !knownPower ? 'POWER UNKNOWN' : s.muted ? 'SOUND MUTED' : '';
  $('volume-pending').textContent = pending && isVolume(active) ? 'Adjusting…' : '\u00a0';
  $('volume-panel').classList.toggle('pending', pending && isVolume(active));
  $('power').classList.toggle('on', awake);
  $('power').setAttribute('aria-label', awake ? 'Put soundbar in standby' : standby ? 'Turn soundbar on' : 'Power state unavailable');
  $('power-label').textContent = active === 'power' ? 'Working…' : awake ? 'Standby' : standby ? 'Turn on' : 'Power unknown';
  $('mute').classList.toggle('active', !!s.muted);
  $('mute').setAttribute('aria-pressed', typeof s.muted === 'boolean' ? String(s.muted) : 'mixed');
  $('mute-label').textContent = active === 'mute' ? 'Changing…' : typeof s.muted !== 'boolean' ? 'Mute state unavailable' : s.muted ? 'Unmute sound' : 'Mute sound';
  $('mute-icon').setAttribute('href', s.muted ? '#i-muted' : '#i-volume');
  $('input-name').textContent = sourceName(s);
  document.querySelectorAll('[data-input]').forEach(b => {
    const selected = b.dataset.input === s.input;
    b.classList.toggle('selected', selected);
    b.setAttribute('aria-pressed', String(selected));
  });
  $('volume-hint').textContent = standby ? 'Turn the bar on to adjust volume.' : !knownPower ? 'Power state is unavailable.' : s.muted ? 'Sound is muted. Tap above to unmute.' : 'Tap to adjust · hold to repeat.';
  buttons.forEach(b => {
    const action = b.dataset.action;
    b.disabled = !ready || !['On', 'Standby'].includes(s.power) || (action !== 'power' && !awake) || (pending && !(isVolume(action) && isVolume(active))) || ((action === 'standard' || action === 'smart') && !['Standard','Smart'].includes(s.mode));
    b.classList.toggle('pending-control', action === active && !isVolume(action) && !segment(b));
    b.setAttribute('aria-busy', String(action === active));
  });
  if (typeof s.muted !== 'boolean') $('mute').disabled = true;
  $('refresh').disabled = pending || refreshing;
  $('cancel-volume').hidden = queue.length === 0;
  updateSync();
  for (const component of components) component.render(context());
  if (/starting|Starting in|Measuring/.test(s.setup?.calibration || '')) for(const button of buttons) button.disabled=true;
  // Right after wake the automation checks the mode, then may restore Standard:
  // the bar refuses native controls meanwhile ("settling"), so say so instead.
  if (ready && s.automation?.enabled && /^(AWAKE_STABILIZING_|APPLYING_STANDARD)/.test(s.automation.status || '')) {
    for (const button of buttons) button.disabled = true;
    $('summary').textContent = s.automation.status === 'APPLYING_STANDARD' || (s.mode === 'Smart' && s.automation.remembered_mode !== 'Smart') ? 'Returning to Standard after wake… controls return in a few seconds.' : 'Checking the sound mode after wake… controls return in a few seconds.';
  }
  capabilities.apply();
}
function updateSync() {
  if (stream?.readyState === EventSource.OPEN && online) { $('last-seen').textContent = 'Live updates'; return; }
  if (!lastSync) return;
  const seconds = Math.floor((Date.now() - lastSync) / 1000);
  $('last-seen').textContent = seconds < 5 ? 'Synced just now' : 'Synced ' + seconds + 's ago';
}
function versioned(s) { return Number.isSafeInteger(s?.revision) && s.revision >= 0; }
function adopt(s) {
  if (versioned(current) && versioned(s) && s.revision < current.revision && (!s.csrf || s.csrf === csrf)) return false;
  const newSession=csrf && s.csrf && csrf!==s.csrf;
  current = s;
  csrf = s.csrf || csrf;
  online = true;
  lastSync = Date.now();
  if(newSession){queue=[];stopHold();clearSyncDraft();playback.resetIntent();void capabilities.load().then(render);}
  return true;
}
function receiveState(s) {
  if (active) { if (!latestState || !versioned(s) || !versioned(latestState) || s.revision >= latestState.revision || s.csrf !== latestState.csrf) latestState = s; return; }
  const reconnecting = !online || !current?.available;
  const changed = settingsChanged(s);
  if (!adopt(s)) return;
  if (!s.available) feedback(s.error || 'Waiting for your soundbar…', 'error');
  else if (reconnecting) feedback('Ready. Your settings update live.');
  else if (changed && $('message').dataset.kind === 'success') feedback('Settings updated from the soundbar.');
  render();
}
function connectStream() {
  if (stream || document.hidden) return;
  const source = new EventSource('/api/events');
  stream = source;
  source.onmessage = event => {
    if (stream !== source) return;
    try { receiveState(JSON.parse(event.data)); } catch { feedback('Unable to read live state. Refresh to reconnect.', 'error'); }
  };
  source.onerror = () => {
    if (stream !== source) return;
    online = false; queue = []; latestState = null; playback.resetIntent(); stopHold(); clearSyncDraft();
    feedback('Live connection lost. Reconnecting; pending taps were cleared.', 'error');
    render();
    if (!current) { $('connection-label').textContent='Reconnecting';$('refresh').disabled=false; }
  };
}
async function refresh() {
  if (refreshing || active || document.hidden) return;
  const version = epoch;
  refreshing = true;
  $('refresh').disabled = true;
  try {
    const response = await fetch('/api/state', {cache: 'no-store', signal: AbortSignal.timeout(8000)});
    if (!response.ok) throw new Error('Unable to reach the soundbar.');
    const s = await response.json();
    if (version !== epoch || active) return;
    const reconnecting = !online || !current?.available;
    const changed = settingsChanged(s);
    adopt(s);
    if (changed && $('message').dataset.kind === 'success') feedback('Settings updated from the soundbar.');
    if (!s.available) feedback(s.error || 'Waiting for your soundbar…', 'error');
    else if (reconnecting) feedback('Ready. Your settings update live.');
  } catch {
    if (version !== epoch || active) return;
    online = false;
    playback.resetIntent();
    queue = [];
    clearSyncDraft();
    stopHold();
    feedback('Connection lost. Reconnecting automatically; no controls will be replayed.', 'error');
  } finally {
    refreshing = false;
    render();
    if (!current) {
      $('connection').dataset.state = 'offline';
      $('connection-label').textContent = 'Offline';
      $('summary').textContent = 'Waiting for your soundbar. Check the local connection.';
      $('refresh').disabled = false;
    }

  }
}
function success(action, s) {
  s=capabilities.state(s);
  if (action === 'eq_apply' && s.eq?.status === 'waiting') return 'Front EQ saved. It is applied when Standard audio plays.';
  if (action === 'eq_reset' || (action === 'eq_apply' && s.eq?.enabled === false)) return 'Original tuning restored.';
  if (action === 'eq_apply') return 'Front EQ applied.';
	if (action === 'night_toggle') return 'Night mode ' + (s.sound?.night ? 'on.' : 'off.');
	if (action === 'media_play') return 'Play requested.';
	if (action === 'media_pause') return 'Pause requested.';
	if (action === 'media_next') return 'Next track requested.';
	if (action === 'media_previous') return 'Previous track requested.';
	if (action === 'media_stop') return 'Stop requested.';
  if (isVolume(action)) return 'Volume ' + s.volume + (s.muted ? ' · sound muted' : '');
  if (action === 'mute') return s.muted ? 'Sound muted.' : 'Sound unmuted.';
  if (action === 'power') return s.power === 'On' ? 'Your soundbar is on.' : 'Soundbar is in standby.';
  if (action === 'bass_next') return 'Bass level ' + (s.sound.bass+1) + ' selected.';
  if (action === 'bass_offset_set') return 'Bass offset saved for startup.';
  if (action === 'timer_start') return 'Timer started.';
  if (action === 'timer_cancel') return 'Timer stopped.';
  if (action === 'timer_extend') return 'Time added.';
  if (action === 'display_message') return 'Message sent to the bar.';
  if (action === 'display_cancel') return 'Message cleared.';
  if (action === 'reboot') return 'The soundbar is restarting. This page reconnects by itself.';
  if (action === 'display_clock') { const c = s.extensions?.['bar91.display-clock']; return !c?.enabled ? 'Clock off.' : c.when === 'always' ? 'Clock on, in standby and with the bar on.' : 'Clock on, in standby.'; }
  if (action === 'center_next') return 'Center level ' + s.sound.center + ' selected.';
  if (action === 'atmos_next') return 'Atmos height: ' + ['Low','Mid','High'][s.sound.atmos] + '.';
  if (action === 'rear_next') return 'Rear speakers: ' + ['Low','Mid','High','Muted'][s.sound.rear] + '.';
  if (action === 'standard') return 'Standard selected. Smart Mode is off.';
  if (action === 'smart') return 'Smart Mode selected.';
  if (action === 'av_sync_set') return 'Audio sync ' + s.sound?.av_sync_ms + ' ms.';
  if (action === 'calibrate') return 'Calibration started. Keep the room quiet.';
  if (action === 'sub_pair') return 'Subwoofer pairing is open. Hold the button on the back of the subwoofer until it flashes white quickly.';
  if (action === 'input_bluetooth') return s.bluetooth?.connected ? 'Bluetooth selected. A device is connected.' : 'Bluetooth selected. Choose ' + (s.bluetooth?.name || capabilities.name()) + ' on your phone.';
  if (action === 'bluetooth_pair') return 'Pairing is open. Choose ' + (s.bluetooth?.name || capabilities.name()) + ' in your phone’s Bluetooth settings.';
  return sourceName(s) + ' selected.';
}
function pendingMessage() {
  if (active === 'eq_apply') return 'Applying front EQ…';
  if (active === 'timer_start') return 'Starting timer…';
  if (active === 'timer_cancel') return 'Stopping timer…';
  if (active === 'timer_extend') return 'Adding time…';
  if (active === 'display_message') return 'Sending the message…';
  if (active === 'display_cancel') return 'Clearing…';
  if (active === 'reboot') return 'Restarting the soundbar…';
  if (active === 'display_clock') return 'Setting the clock…';
  if (active === 'eq_reset') return 'Restoring original tuning…';
	if (active === 'night_toggle') return 'Changing night mode… audio may briefly mute.';
	if (active?.startsWith('media_')) return 'Sending playback control…';
  if (isVolume(active)) return 'Adjusting volume' + (queue.length ? ' · ' + queue.length + ' queued' : '') + '…';
  if (active === 'standard') return 'Selecting Standard… audio may briefly mute.';
  if (active === 'smart') return 'Selecting Smart Mode… audio may briefly mute.';
  if (active === 'av_sync_set') return 'Adjusting audio sync to ' + advanced.target() + ' ms… other controls wait a few seconds.';
  if (active === 'calibrate') return 'Starting calibration…';
  if (active === 'sub_pair') return 'Opening subwoofer pairing…';
  if (active === 'input_bluetooth') return 'Switching to Bluetooth…';
  if (active === 'bluetooth_pair') return 'Starting Bluetooth pairing…';
  return active === 'power' ? 'Changing power state…' : 'Applying your change…';
}
function clearSyncDraft(){clearTimeout(syncTimer);syncTimer=null;advanced.clear();}
// One submit path for the sync draft: taps wait until they stop, a typed
// value (Enter or blur) goes at once. Both become a single av_sync_set.
function submitSync(delay){
  clearTimeout(syncTimer);
  syncTimer=setTimeout(()=>{
    syncTimer=null;
    if(!online || active || advanced.target()===current?.sound?.av_sync_ms){advanced.clear();render();return;}
    if(!queue.includes('av_sync_set')) queue.push('av_sync_set');
    void pump();
  },delay);
  render();
}
function avSyncTap(action){
  if(!advanced.adjust(action,current)) return;
  submitSync(900);
  feedback('Audio sync '+advanced.target()+' ms · sending when you stop tapping…','pending');
}
function avSyncTyped(){
  if(!csrf || active) return;
  if(advanced.type(current)) submitSync(0);
  else {clearTimeout(syncTimer);syncTimer=null;render();}
}
// A segment (level or mode) becomes the confirmed steps that reach it, run
// one by one through the same queue and capability mapping as other controls.
const segment = button => button.dataset.value !== undefined || button.dataset.mode !== undefined;
function choose(button) {
  if (button.disabled || !csrf || active || queue.length) return;
  clearSyncDraft();
  const steps = sound.plan(button, capabilities.state(current));
  if (!steps) return;
  for (let i = 0; i < steps; i++) queue.push(button.dataset.action);
  void pump();
}
function enqueue(action) {
  const button = buttons.find(b => b.dataset.action === action);
  if (!button || button.disabled || !csrf) return;
  if (action === 'eq_apply' && !equalizer.gains()) return;
  if(action==='av_sync_up' || action==='av_sync_down'){avSyncTap(action);return;}
  clearSyncDraft();
  if (isVolume(action) && queue.length && delta(queue[queue.length - 1]) === -delta(action)) queue.pop();
  else if (queue.length < 8) queue.push(action);
  else { feedback('Let the pending volume changes finish first.', 'pending'); return; }
  if (active) { render(); feedback(pendingMessage(), 'pending'); }
  else void pump();
}
async function pump() {
  if (active || !queue.length || !online) return;
  active = queue.shift();
  const gains = active === 'eq_apply' ? equalizer.gains() : null;
  const parameters=active==='bass_offset_set'?bassOffset.parameters():active?.startsWith('timer_')?timer.parameters(active):active?.startsWith('display_')?messages.parameters(active):active==='reboot'?restart.parameters(active):null;
  const syncTarget=active==='av_sync_set'?advanced.target():null;
  epoch++;
  render();
  feedback(pendingMessage(), 'pending');
  const action = active;
  const requestPeer = playback.identity(current);
  try {
    const response = await fetch('/api/action', {method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf}, body: JSON.stringify(capabilities.command(action,gains,syncTarget,parameters)), signal: AbortSignal.timeout(action==='av_sync_set'?70000:action.startsWith('eq_')?35000:30000)});
    const text = await response.text();
    let data;
    try { data = JSON.parse(text); } catch { throw new Error(text.trim() || 'Control unavailable. Refresh and try again.'); }
    if (!response.ok) {
      if (data.state && 'available' in data.state) adopt(data.state);
      throw new Error(data.error || 'Control unavailable.');
    }
    adopt(data);
    if (!data.available) throw new Error(data.error || 'Soundbar unavailable.');
    playback.accepted(action,requestPeer,data);
    if(action==='timer_start') timer.started(parameters);
    if(action==='display_message') messages.sent(parameters);
    feedback(success(action, data), 'success');
  } catch (error) {
    if(action.startsWith('timer_')) timer.failed(action);
    if(action.startsWith('display_')) messages.failed();
    queue = [];
    clearSyncDraft();
    stopHold();
    if (error.name === 'TypeError' || error.name === 'TimeoutError') {
      online = false;
      playback.resetIntent();
      feedback('Connection interrupted. The last change may have applied; pending taps were cleared.', 'error');
    } else feedback(error.message, 'error');
  } finally {
    if(action==='av_sync_set') clearSyncDraft();
    active = null;
    render();
    if (latestState) {const next=latestState;latestState=null;receiveState(next);}
    if (queue.length && online) void pump();
  }
}
function stopHold() {
  clearTimeout(holdTimer);
  clearInterval(repeatTimer);
  if (heldButton) heldButton.classList.remove('pressed');
  heldButton = null;
}
buttons.forEach(button => {
  button.addEventListener('click', event => {
    // Pointer input is handled on down; keyboard and assistive clicks have detail 0.
    if (segment(button)) choose(button);
    else if (!isVolume(button.dataset.action) || event.detail === 0) enqueue(button.dataset.action);
  });
  if (!isVolume(button.dataset.action)) return;
  button.addEventListener('pointerdown', event => {
    if (event.button !== 0 || button.disabled) return;
    event.preventDefault();
    stopHold();
    button.focus({preventScroll: true});
    button.setPointerCapture(event.pointerId);
    heldButton = button;
    button.classList.add('pressed');
    enqueue(button.dataset.action);
    holdTimer = setTimeout(() => {
      repeatTimer = setInterval(() => {
        if (queue.length < 1 && !button.disabled) enqueue(button.dataset.action);
      }, 180);
    }, 400);
  });
  for (const event of ['pointerup', 'pointercancel', 'lostpointercapture']) button.addEventListener(event, stopHold);
});
document.addEventListener('keydown', event => {
  if (event.altKey || event.ctrlKey || event.metaKey || event.target.closest('input,textarea,select,[contenteditable="true"]')) return;
  const action = {'ArrowUp': 'volume_up', 'ArrowDown': 'volume_down', m: 'mute', M: 'mute'}[event.key];
  if (!action || (event.repeat && (!isVolume(action) || queue.length >= 1))) return;
  event.preventDefault();
  enqueue(action);
});
window.addEventListener('blur', stopHold);
document.addEventListener('visibilitychange', () => {
  stopHold();
  if (document.hidden) {stream?.close();stream=null;} else connectStream();
});
$('av-sync-input').addEventListener('change', avSyncTyped);
$('av-sync-input').addEventListener('keydown', event => { if (event.key === 'Enter') { event.preventDefault(); event.currentTarget.blur(); } });
$('refresh').addEventListener('click', () => { connectStream(); void refresh(); });
$('cancel-volume').addEventListener('click', () => {
  queue = [];
  clearSyncDraft();
  stopHold();
  render();
  feedback(active ? 'Queued taps cleared. Finishing the current step…' : 'Queued taps cleared.', active ? 'pending' : '');
});

// One capability discovery request, one state stream, one serialized action queue.
await capabilities.load();
connectStream();
setInterval(()=>{if(current)system.tick(context());},2000);
