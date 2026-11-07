import {byId} from './dom.js';
export function createEqualizer(root) {
  const $ = id => byId(root,id);
  let current, online=false, active=null, queued=0, csrf='';
  let eqDraft=null, eqCommitted='';
  const eqSliders=Array.from({length:5},(_,i)=>$('eq-slider-'+i));
  const eqPresets = Object.freeze({
    original: Object.freeze([0,0,0,0,0]),
    less_bass: Object.freeze([-4,-2,0,0,0]),
    clear_dialogue: Object.freeze([-3,-1,0,0,-1]),
    softer_treble: Object.freeze([0,0,0,-2,-3]),
    warm: Object.freeze([0,0,-1,-2,-3]),
    bright: Object.freeze([-3,-2,-1,0,0]),
    vocal_focus: Object.freeze([-4,-2,0,-1,-2]),
  });
  function validEQGains(gains) {
    return Array.isArray(gains) && gains.length === 5 && gains.every(value => Number.isFinite(value) && value >= -6 && value <= 0 && Number.isInteger(value * 2));
  }
  function eqGain(value) { return (value === 0 ? '0' : value.toFixed(1).replace(/\.0$/, '')) + ' dB'; }
  function renderEQ(s, ready, awake) {
    const eq = s.eq || {};
    const valid = validEQGains(eq.gains_db);
    const key = JSON.stringify([s.csrf || csrf, eq.enabled, valid ? eq.gains_db : null]);
    if (key !== eqCommitted) { eqCommitted = key; eqDraft = valid ? eq.gains_db.slice() : null; }
    const draftValid = validEQGains(eqDraft);
    const changed = valid && draftValid && eqDraft.some((value, index) => value !== eq.gains_db[index]);
    const pending = !!active || queued > 0;
    // A remote that keeps the EQ (eq.kept reported) also saves one it cannot
    // apply now (standby, Smart Mode, another audio route) and applies it
    // once Standard audio plays, so the EQ stays adjustable.
    const keeps = typeof eq.kept === 'boolean' && eq.error !== 'EQ support is not installed';
    const applyNow = awake && eq.available === true;
    const saveLater = !applyNow && keeps;
    const canAdjust = ready && (applyNow || saveLater) && valid && !pending;
    // Original (all bands at 0 dB) over an applied or kept EQ turns it off:
    // the server resets and forgets it. There is no separate Reset button.
    const turnOff = draftValid && eqDraft.every(value => value === 0) && (eq.enabled === true || eq.kept === true);
    $('eq-preset').disabled = !canAdjust;
    $('eq-preset').value = draftValid ? Object.keys(eqPresets).find(name => eqPresets[name].every((value, index) => value === eqDraft[index])) || 'custom' : 'custom';
    for (const [index, input] of eqSliders.entries()) {
      input.disabled = !canAdjust;
      input.value = draftValid ? eqDraft[index] : 0;
      input.setAttribute('aria-valuetext', draftValid ? eqGain(eqDraft[index]) : 'Unavailable');
      $('eq-gain-' + index).textContent = draftValid ? eqGain(eqDraft[index]) : '—';
    }
    $('eq-apply').disabled = !canAdjust || !changed;
    $('eq-apply').textContent = active === 'eq_apply' || active === 'eq_reset' ? (turnOff || active === 'eq_reset' ? 'Turning off…' : saveLater ? 'Saving…' : 'Applying…') : turnOff && changed ? 'Turn off EQ' : saveLater ? 'Save EQ' : 'Apply EQ';
    $('eq-panel').dataset.state = !ready ? 'offline' : eq.status === 'updating' ? 'updating' : saveLater && valid ? (changed ? 'draft' : 'waiting') : !eq.available || !valid ? 'unavailable' : changed ? 'draft' : eq.enabled ? 'custom' : 'original';
    // A saved non-zero bass offset does not run while an EQ is applied.
    const offsetPaused = s.bass_offset?.error === 'WAITING_FOR_FRONT_EQ' && s.bass_offset.offset_db !== 0 ? ' Bass offset paused meanwhile.' : '';
    const notNow = !awake ? (s.power === 'Standby' ? 'The bar is in standby.' : 'Power state is unavailable.') : s.mode === 'Smart' ? 'Smart Mode is on.' : 'The EQ does not apply on this audio route.';
    // Status at a glance: is the EQ heard now, waiting to apply, or off?
    // "Active" needs the native confirmation (status "active"): eq.enabled
    // alone stays true while the bar is in Smart Mode or on another route,
    // where the applied EQ is not heard and is applied again afterwards.
    const badge = $('eq-badge');
    const shown = ready && valid && eq.error !== 'EQ support is not installed';
    const confirmed = eq.status === 'active' || (eq.enabled === true && eq.status === undefined);
    // "updating": the bar changed its processing and the remote follows it
    // within seconds. There is no "reset needed" state for the owner.
    const kind = eq.status === 'updating' ? 'updating' : eq.enabled === true ? (confirmed ? 'active' : 'waiting') : eq.status === 'waiting' ? 'waiting' : 'off';
    badge.hidden = !shown;
    badge.dataset.state = kind;
    badge.textContent = {active: 'Active', waiting: 'Waiting', off: 'Off', updating: 'Updating'}[kind];
    badge.title = {active: 'Your EQ is applied and heard now.', waiting: notNow + ' Your EQ is applied when Standard audio plays.', off: 'Original tuning: no EQ is applied.', updating: 'The bar changed its sound processing; your EQ is updated automatically.'}[kind];
    badge.setAttribute('aria-label', 'Front EQ status: ' + badge.textContent + '. ' + badge.title);
    $('eq-state').textContent = !online ? 'Offline. Reconnect to adjust EQ.' : !ready ? 'Waiting for the soundbar.' : saveLater ? (active === 'eq_apply' ? 'Saving your settings…' : changed ? (turnOff ? 'Select Turn off EQ to keep the original sound.' : 'Unsaved changes. Select Save EQ: it is applied when Standard audio plays.') : eq.status === 'waiting' && eq.error ? eq.error : notNow + ' Choose an EQ now: it is applied when Standard audio plays.') : !awake ? (s.power === 'Standby' ? 'Turn on the bar to adjust EQ.' : 'Power state is unavailable.') : eq.error ? eq.error : !eq.available || !valid ? 'EQ is unavailable on this firmware or audio path.' : active === 'eq_apply' ? 'Applying your settings…' : active === 'eq_reset' ? 'Restoring original processing…' : changed ? (turnOff ? 'Select Turn off EQ to restore the original sound.' : 'Unapplied changes. Select Apply EQ to listen.') : eq.enabled ? (eq.kept ? 'Custom EQ active. It stays through sound, input and power changes. Choose Original to turn it off.' : 'Custom EQ active. Choose Original to turn it off.') + offsetPaused : 'Original tuning. Adjust a band, then apply.';
    $('eq-graph').hidden = !draftValid;
    if (draftValid) {
      const frequencies = Array.isArray(eq.frequencies_hz) && eq.frequencies_hz.length === 5 && eq.frequencies_hz.every(value => Number.isFinite(value) && value > 0) ? eq.frequencies_hz : [125,500,2000,4000,8000];
      const points = eqDraft.map((gain, index) => [10 + Math.log2(frequencies[index] / 125) / 6 * 280, 8 - gain * 8]);
      const line = 'M' + points.map(point => point.join(',')).join('L');
      $('eq-gain-line').setAttribute('d', line);
      $('eq-gain-area').setAttribute('d', line + 'L290,8L10,8Z');
      $('eq-graph').querySelector('svg').setAttribute('aria-label', (changed ? 'Unapplied' : eq.enabled ? 'Applied' : eq.status === 'waiting' ? 'Saved' : 'Original') + ' EQ gain settings: ' + eqDraft.map(eqGain).join(', '));
    }
  }
  for (const [index, input] of eqSliders.entries()) input.addEventListener('input', () => {
    if (input.disabled || !validEQGains(eqDraft)) return;
    const next = eqDraft.slice(); next[index] = Number(input.value);
    if (!validEQGains(next)) return;
    eqDraft = next;
    renderEQ(current, online && current.available, current.power === 'On');
  });

  $('eq-preset').addEventListener('change', () => {
    const preset = $('eq-preset').value;
    if ($('eq-preset').disabled || !Object.hasOwn(eqPresets, preset)) return;
    eqDraft = eqPresets[preset].slice();
    renderEQ(current, online && current.available, current.power === 'On');
  });

  return {
   render(context) { ({state:current,online,active,queued,csrf}=context); renderEQ(current,context.ready,context.awake); },
   gains() { return validEQGains(eqDraft) ? eqDraft.slice() : null; }
  };
}
