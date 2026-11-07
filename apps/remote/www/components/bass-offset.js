import {byId} from './dom.js';
const valid = db => Number.isFinite(db) && db >= -3 && db <= 3 && Number.isInteger(db * 2);
const format = db => (db > 0 ? '+' : '') + db.toFixed(1) + ' dB';
// The monitor reports why it waits as a code; say it in words. A code with
// its own sentence replaces the generic waiting one.
const reasons = {
  WAITING_FOR_STANDARD_PLAYBACK: '',
  WAITING_FOR_FRONT_EQ: 'Paused while a Front EQ is applied. Turn off the Front EQ to use the bass offset.',
  WAITING_FOR_CALIBRATION: 'Paused during surround calibration.',
  WAITING_FOR_NATIVE_CONTROLS: 'Waiting for the current control to finish.',
  WAITING_FOR_AUDIO_ROUTE: 'Waiting for audio playback.',
  WAITING_FOR_SUPPORTED_DSP_MODE: 'Not supported in this sound mode or on this input.',
};
function explain(b) {
  const reason = Object.hasOwn(reasons, b.error || '') ? reasons[b.error] : null;
  if (reason) return ' ' + reason;
  const status = b.status === 'waiting' ? ' Waiting for supported Standard playback.' : b.status === 'paused' ? ' Automatic adjustment paused.' : '';
  return status + (b.error && reason === null ? ' ' + b.error : '');
}
export function createBassOffset(root) {
  const $ = id => byId(root,id);
  const select = $('bass-offset');
  let committed = '', draft = 0, latest;
  for (let db = 3; db >= -3; db -= 0.5) {
    const option = select.ownerDocument.createElement('option');
    option.value = String(db); option.textContent = format(db); select.append(option);
  }
  function render(context) {
    latest = context;
    const {state:s,ready,active,queued,csrf} = context;
    const b = s.bass_offset, db = b?.offset_db;
    const known = valid(db);
    const key = JSON.stringify([s.csrf || csrf,known ? db : null]);
    if (key !== committed) { committed = key; draft = known ? db : 0; }
    select.value = String(draft);
    const pending = !!active || queued > 0;
    const calibrating = /starting|Starting in|Measuring/.test(s.setup?.calibration || '');
    select.disabled = !ready || !known || pending || calibrating;
    $('bass-offset-save').disabled = select.disabled;
    $('bass-offset-save').textContent = active === 'bass_offset_set' ? 'Saving…' : 'Save offset';
    const changed = known && draft !== db;
    $('bass-offset-state').textContent = !known ? 'Unavailable' : !ready ? 'Last saved: ' + format(db) + '. Connection unavailable.' : changed ? format(draft) + ' selected. Select Save offset to keep it.' : format(db) + ' set. ' + (b.status === 'original' ? 'Original tuning.' : 'Saved for startup.') + explain(b);
  }
  select.addEventListener('change', () => {
    const value = Number(select.value);
    if (select.disabled || !valid(value)) return;
    draft = value; if (latest) render(latest);
  });
  return {render, parameters: () => valid(draft) ? {offset_db:draft} : null};
}
