# Timers (Bar 9.1)

**Experimental.** The web remote can use the bar as a kitchen timer. When the time is up, `TIME'S UP` and the timer's name scroll on the front display until you press any key on the JBL remote. It works with the bar on and in standby, without waking it. Timers live in the remote: a restart of the remote or the bar clears them.

Qualified on one Bar 9.1 (firmware 23.06.41.80) on 10 November 2026, first with `vfdtest.sh`, a shell prototype, then with this feature on the bar:

- **The key flag** rises on every key press, also in standby.
- **The countdown** works only while the text is short. `4'59` rewritten in place stays still. `TIMER! 4'59` is longer than the display, so it scrolls, and each rewrite restarts the scroll before the time can be read.
- **Characters.** The display does not show `:` or `!` correctly.

## In the page

The **Timers** panel has:

- **a name**, optional, for example `PIZZA`. As for display messages, it is upper-cased and accents are folded. Only letters, digits, space and `- . '` are allowed;
- **a duration** in minutes, from 1 to 720, with 1, 5, 10 and 15 shortcuts;
- **Show the time left on the bar**:
  - **one timer running**: its clock, second by second, in place: `4'59`, then `1H04'59` past an hour. No name, so that it does not scroll;
  - **several running**: once a minute, a round where each timer with the option shows `12 MIN LEFT LESSIVE`, then `4 MIN LEFT PIZZA`, in turn. The time comes first so that it does not read as the end, and minutes are rounded to the nearest. Rounds follow the whole minutes of the first timer to end that has a minute or more left;
  - the display switches between the two as timers start and end.

Running timers are listed with their time left and a Cancel button. A ringing timer offers Stop. The panel works with the bar in standby.

## Rules

- **At most 8 timers.** Two timers cannot share a name, and only one may have no name: the display would not tell them apart, and the page says so.
- **The clock** is a display message whose text is rewritten in place, with no blanks between passes (display hooks `Live` and `Loop`). Like any long message, it lets other messages through between passes.
- **Reminders** are one-pass messages under the timer's display id: a new one replaces one that has not finished.
- **When a timer ends**, `TIME'S UP PIZZA` scrolls in a loop, without blanks. A timer without a name shows `TIME'S UP - PRESS ANY KEY`. A name too long for both shows alone. A short text alone would not scroll, only blink between blanks, which is less striking. The alarm lasts until one of these:
  - **any key on the JBL remote** (volume, mute, source…), on or in standby. The remote watches the bar's key flag (`13822224`), which rises on every press, also in standby, while nothing changes on the display. Only a press seen after a release counts: a key held when the alarm starts, or one the remote sends itself while it holds the action gate, does not stop it;
  - **Stop** in the page, or `bar91.timer.cancel`;
  - **10 minutes.**

  Several timers that end together take turns on the display, like messages. A key stops the one being shown.
- **The bar's own messages come first**, like for display messages: volume, `HELLO` on wake-up and so on.
- **`display_cancel`** without an id stops display messages, not timers.

## API

`GET /api/features` lists the `bar91.timer` extension. The state carries the timers under `extensions["bar91.timer"].timers`. Each entry has `id`, `text` (empty without a name), `countdown`, `remaining_ms` (as of that state) and `ringing`.

```json
{"action":"bar91.timer.start","parameters":{"text":"Pizza","duration_s":600,"countdown":true}}
{"action":"bar91.timer.start","parameters":{"duration_s":300}}
{"action":"bar91.timer.cancel","parameters":{"id":"t1"}}
```

The short aliases are `timer_start` and `timer_cancel`.
- **Start:** `duration_s` is required (1 to 43200). `text` and `countdown` are optional.
- **Refused:** an invalid name, a name already in use (or a second timer without a name), a ninth timer, an unknown id and unknown fields.

## Tests

`timer_test.go` drives the timers on the simulated display (`display_message_test.go`). It covers:
- the texts: clock (`4'59`, `1H04'59`), reminder (`4 MIN LEFT PIZZA`) and alarm (`TIME'S UP PIZZA`, `TIME'S UP - PRESS ANY KEY`), all within the display rules;
- parameter validation and reserved ids;
- one timer: the clock second by second, without blanks, then the alarm;
- two timers:
  - minute rounds in turn, with the clock stopping;
  - the clock coming back when one is cancelled;
  - the timer named in reminders when only one has the option;
- an alarm looping without blanks, stopped by a key in standby, with the display handed back;
- a key held before the alarm, which does not count;
- the 10-minute limit;
- duplicate names, a single unnamed timer, cancel, and `display_cancel` leaving timers alone;
- two alarms taking turns;
- the limit of 8 timers.

`tests/webremote-ui.mjs` covers the panel: a name or none, a duplicate refused, standby, a ringing timer, and 320 px.
