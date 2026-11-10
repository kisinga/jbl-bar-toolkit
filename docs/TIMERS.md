# Timers (Bar 9.1)

**Experimental.** The web remote can use the bar as a kitchen timer. When the time is up, `TIME'S UP` and the timer's name scroll on the front display until you press any key on the JBL remote. It works with the bar on and in standby, without waking it. Timers live in the remote: a restart of the remote or the bar clears them.

Qualified on one Bar 9.1 (firmware 23.06.41.80) on 10 November 2026, first with `vfdtest.sh`, a shell prototype, then with this feature on the bar:

- **The key flag** rises on every key press, also in standby.
- **The countdown** works only while the text is short. `4'59` rewritten in place stays still. `TIMER! 4'59` is longer than the display, so it scrolls, and each rewrite restarts the scroll before the time can be read.
- **Characters.** The display does not show `:` or `!` correctly.

## In the page

**At the top of the page**, beside the title, a pill shows the timers at a glance: one ring per timer (three at most, then `+2`), filling as the end nears, with the name of a lone timer or `2 timers`, and the next end (`4:59`, `next 4:59`). When one rings, it turns orange and reads `Time's up`. A tap leads to the Timers panel.

The **Timers** panel reads from top to bottom:

1. **Running timers**, first to end first. Each one shows a ring that fills as the end nears (time added empties it a little), its name, when it ends, its options, and the time left (`4:59`), with **+1 min** under it, and **×** to cancel it. A ringing timer goes on top, in orange, with a large **Stop** and "or press any key on the JBL remote".
   With a timer running, everything below folds behind **Add another timer**, since several timers at once are the exception; it opens on the minutes, and × folds it again. It folds after each start, and opens by itself when no timer is left.
2. **Start again**: the last four timers started from this device, in a tinted box. One tap starts one again, with its options. A timer that is running cannot start twice, so its button is off meanwhile.
3. **New timer**:
   - **the minutes**, large, with − and + and the shortcuts 1, 3, 5, 10, 15 and 30. They can be typed, from 1 to 720;
   - **a name**, optional, for example `Pizza`. As for display messages, the bar shows it upper-cased, with accents folded. Only letters, digits, space and `- . '` are allowed. A name in use is refused under the field, with a suggestion (`Pizza 2`);
   - **Options**, folded, with a summary of the choices (`Chime unless watching TV · no countdown`):
     - **Countdown on the bar**:
       - **one timer running**: its clock, second by second, in place: `4'59`, then `1H04'59` past an hour. No name, so that it does not scroll;
       - **several running**: once a minute, a round where each timer with the option shows its name and time left, in turn: `LESSIVE 12'57`, then `PIZZA 4'59`. Each time is taken when its own pass starts, so it is not stale by the earlier passes of the round, and the last minute is included. Rounds follow the whole minutes of the first timer to end that has a minute or more left;
       - the display switches between the two as timers start and end;
     - **Chime at the end**: **Off**, **Not on TV** (the default: silent while the bar is on with TV, optical or HDMI, so a film is never cut) or **Always** (even during a film);
     - with a chime, **Volume at least**, 1 to 30, 10 by default: the bar is unmuted and turned up to this volume if it is lower, never down;
     - and **Rings for**: 10 s, 30 s (default), 1 min or 2 min. The chime repeats this long, then the bar returns to its volume, source or standby. The `TIME'S UP` text goes on until a key.

     The options are kept for the next timers on this device;
   - **Start** names what it starts (`Start 5-minute timer`);
   - under it, folded, **Rings at 18:47 · what will happen?** Unfolded, three lines say what the bar does now, at the end (the text that scrolls, the chime) and how to stop it.
4. After a start, the form folds and the page scrolls to the new timer, outlined for a moment. When a timer without a name runs, the empty name is a calm hint (give this one a name), not an error. A toast says `Pizza started. Rings at 18:47.` for 8 seconds, with **Undo**, which cancels it.

Recent timers and options live in the browser's storage: each device has its own. The panel works with the bar in standby.

## Rules

- **At most 8 timers.** Two timers cannot share a name, and only one may have no name: the display would not tell them apart, and the page says so.
- **Display updates follow the timers.** The countdown loop decides from a snapshot; its updates are serialized with the transitions that replace or remove them (ringing, cancel), and it checks the timer still counts down first. So a late countdown never replaces an alarm (which would end the timer) nor shows after a cancel.
- **The clock** is a display message whose text is rewritten in place, with no blanks between passes (display hooks `Live` and `Loop`). Like any long message, it lets other messages through between passes.
- **Reminders** are one-pass messages under the timer's display id, their text taken when the pass starts (display hook `Fresh`): a new one replaces one that has not finished.
- **When a timer ends**, `TIME'S UP PIZZA` scrolls in a loop, without blanks. A timer without a name shows `TIME'S UP - PRESS ANY KEY`. A name too long for both shows alone. A short text alone would not scroll, only blink between blanks, which is less striking. The alarm lasts until one of these:
  - **any key on the JBL remote** (volume, mute, source…), on or in standby. The remote watches the bar's key flag (`13822224`), which rises on every press, also in standby, while nothing changes on the display. Only a press seen after a release counts, so a key held when the alarm starts does not stop it. The flag also rises for the keys the remote sends itself (to restore the source or standby, for example): the remote notes when it sends one, and a press then does not count. A press made while another control holds the gate for a moment (the monitors take it every second) still counts, and so does the press that brings up the bar's volume over the alarm. On the owner's bar (10 November 2026), both were sometimes lost, and a second press was needed;
  - **Stop** in the page, or `bar91.timer.cancel`;
  - **10 minutes.**

  Several timers that end together take turns on the display, like messages. A key stops the one being shown; the next key, after a release, stops the next one.

  Keys also count while the alarm waits behind the bar's own screen. On the owner's bar (10 November 2026), a volume key that stopped the first of two alarms brought up the volume, and each further press kept it up, so the second alarm never came back on screen to see a key. A key pressed while another alarm of ours is shown belongs to that one.
- **The sound** is a "dring dring" chime (about 2.8 s, a WAV made by the remote itself). The remote serves it for the time of the sound, on a short-lived local address with a random path. It then asks the bar's own Cast receiver (`127.0.0.1:8009`, Default Media Receiver) to play it, trying the loopback address first, then the bar's network address. This was qualified with `catt` on 10 November 2026:
  - the bar plays it on or from standby, at its own volume;
  - waking from standby does not wake the TV;
  - afterwards, the bar stays on Chromecast and stays on.

  **Volume.** The bar's volume is the Cast master volume: native ≈ Cast level × 32 (0.50 gave 16, 0.34 gave 11). The remote sets it through the Cast receiver and reads the native volume back, correcting by a step or two. It does this after the launch and before the chime, while a reader of its own keeps answering the Cast heartbeats, so the chime rings at its volume from the start: once the sound plays, the volume no longer moves by itself, and a key stops the alarm (owner's choice). It unmutes if needed and never lowers the volume.

  **Ringing.** The chime is loaded again each time it finishes, until the ringing time has passed. A key on the JBL remote, or Stop, ends the timer and its sound together.

  **The action gate.** The changes to the bar go through the action gate, like any control, reported as "Timer sound", so they do not interleave with an EQ change or another control: first the Cast launch (which switches the source and may wake the bar) and the volume, then, afterwards, the restore. The chime rings without the gate. While the timer sound holds the gate for the launch and the volume, it lets the display messages through: those changes go through Cast and press no key, so the alarm keeps showing and a key pressed meanwhile still stops it. On the owner's bar (10 November 2026), presses during the volume change were otherwise ignored. During the restore (which may send keys), keys are not watched. Stop never waits for the gate. If another control keeps the bar busy for 10 seconds when a timer ends, the sound is skipped and the alarm shows alone.

  **Afterwards**, the remote puts everything back, even when the sound was stopped early: volume and mute, then standby if the bar was in standby, or its source (TV, HDMI, Bluetooth). What it puts back is recorded before each change, so a change that failed half-way, or was stopped, is undone too. Timers that end together ring once. A Chromecast or AirPlay source is left as it is.
- **The bar's own messages come first**, like for display messages: volume, `HELLO` on wake-up and so on.
- **`display_cancel`** without an id stops display messages, not timers.

## API

`GET /api/features` lists the `bar91.timer` extension. The state carries the timers under `extensions["bar91.timer"].timers`. Each entry has `id`, `text` (empty without a name), `countdown`, `sound`, `sound_volume`, `ring_s`, `remaining_ms` (as of that state), `duration_ms` (from the start to the end, time added included) and `ringing`.

```json
{"action":"bar91.timer.start","parameters":{"text":"Pizza","duration_s":600,"countdown":true,"sound":"unless_tv"}}
{"action":"bar91.timer.start","parameters":{"duration_s":300}}
{"action":"bar91.timer.extend","parameters":{"id":"t1","add_s":60}}
{"action":"bar91.timer.cancel","parameters":{"id":"t1"}}
```

The short aliases are `timer_start`, `timer_extend` and `timer_cancel`.
- **Start:** `duration_s` is required (1 to 43200). `text`, `countdown`, `sound` (`off`, the default for the API, `unless_tv` or `always`), `sound_volume` (1 to 30, default 10) and `ring_s` (3 to 300, default 30) are optional.
- **Extend:** `add_s`, 1 to 3600, adds time to a running timer, up to 12 hours left. A ringing timer is not extended.
- **Refused:** an invalid name, a name already in use (or a second timer without a name), a ninth timer, an unknown id and unknown fields.

## Tests

`timer_test.go` drives the timers on the simulated display (`display_message_test.go`). It covers:
- the texts: clock (`4'59`, `1H04'59`), reminder (`PIZZA 4'34`) and alarm (`TIME'S UP PIZZA`, `TIME'S UP - PRESS ANY KEY`), all within the display rules;
- parameter validation and reserved ids;
- one timer: the clock second by second, without blanks, then the alarm;
- two timers:
  - minute rounds in turn, each time taken when its pass starts, with the clock stopping;
  - the clock coming back when one is cancelled;
  - the timer named in reminders when only one has the option;
- an alarm looping without blanks, stopped by a key in standby, with the display handed back;
- a key held before the alarm, which does not count;
- the 10-minute limit;
- duplicate names, a single unnamed timer, cancel, and `display_cancel` leaving timers alone;
- two alarms taking turns, one key each, also when the second waits behind the bar's volume screen;
- a press during another control's short hold of the gate, and the press that brings up the volume, count; the remote's own keys do not;
- the limit of 8 timers;
- time added to a running timer: the clock follows and the alarm comes later; a ringing timer, an unknown id and more than 12 hours are refused;
- the sound (`timer_sound_test.go`):
  - the chime is a valid WAV of about 2.8 s, without clipping;
  - the Cast exchange with a simulated receiver: launch, heartbeat, the load after the volume (heartbeats answered meanwhile), played to the end, a refused load, and ringing again until the time is up;
  - the TV and restore rules;
  - the sound played once when its timer rings, and not for a timer without sound;
  - the parameters (mode, volume, ringing time, with defaults);
  - the volume (`timer_volume_test.go`): raised with a correction, never lowered, and put back with the mute after a change stopped half-way.
- races (`timer_test.go`): a countdown held until the timer rings does not replace the alarm, and one held until a cancel does not show.

`tests/webremote-ui.mjs` covers the panel:
- the folded options and what will happen, and their summaries;
- the minutes, presets and Start's label;
- a name or none, a duplicate refused under the field;
- Undo, recent timers (started again in one tap, kept after a reload, the last first), options kept;
- the form folded behind Add another timer while timers run, opened and closed again;
- the ring filling as the end nears, and emptying a little with time added;
- the rings at the top (count, label, next end, time's up) and their link to the panel;
- the scroll to the timer just started, +1 min under the time left, × to cancel;
- standby, a ringing timer with Stop, and 320 px.
