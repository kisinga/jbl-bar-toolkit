# Timers (Bar 9.1)

**Experimental.** The web remote can use the bar as a kitchen timer. When the time is up, `TIME'S UP` and the timer's text scroll on the front display until you press any key on the JBL remote. It works with the bar on and in standby, without waking it. Timers live in the remote: a restart of the remote or the bar clears them.

Qualified on one Bar 9.1 (firmware 23.06.41.80) on 10 November 2026: the key flag with `vfdtest.sh`, a shell prototype; then the first version of this feature on the bar. A countdown rewritten every second did not work, because a text longer than the display scrolls and each rewrite restarts it. It became a once-a-minute reminder.

## In the page

The **Timers** panel has:

- a text, `TIMER` by default. As for display messages, it is upper-cased and accents are folded; only letters, digits, space and `- . '` are allowed, since the display does not show `:` or `!` correctly;
- a duration in minutes, from 1 to 720, with 1, 5, 10 and 15 shortcuts;
- **Show the minutes left on the bar, once a minute**: at each whole minute left, one pass, time first so that it does not read as the end: `4 MIN` when one timer runs, `4 MIN LEFT PATES` when several do. Nothing is shown at the start or during the last minute.

Running timers are listed with their time left and a Cancel button. A ringing timer offers Stop. The panel works with the bar in standby.

## Rules

- **At most 8 timers.** Two timers cannot share a text, because the display would not tell them apart; the page says so.
- **Reminders** are ordinary one-pass messages under the timer's display id: a new one replaces one that has not finished.
- **When a timer ends**, `TIME'S UP PATES` scrolls in a loop, without the blank between passes. A short text alone would not scroll and would only blink between blanks, which is less striking. A name too long for both shows alone. It lasts until one of these:
  - **any key on the JBL remote** (volume, mute, source…), on or in standby. The remote watches the bar's key flag (`13822224`), which rises on every press, also in standby, while nothing changes on the display. Only a press seen after a release counts: a key held when the alarm starts, or one the remote sends itself while it holds the action gate, does not stop it;
  - **Stop** in the page, or `bar91.timer.cancel`;
  - **10 minutes.**

  Several timers that end together take turns on the display, like messages. A key stops the one being shown.
- **The bar's own messages come first**, like for display messages: volume, `HELLO` on wake-up and so on.
- **`display_cancel`** without an id stops display messages, not timers.

## API

`GET /api/features` lists the `bar91.timer` extension. The state carries the timers under `extensions["bar91.timer"].timers`. Each entry has `id`, `text`, `countdown`, `remaining_ms` (as of that state) and `ringing`.

```json
{"action":"bar91.timer.start","parameters":{"text":"Pâtes","duration_s":600,"countdown":true}}
{"action":"bar91.timer.cancel","parameters":{"id":"t1"}}
```

The short aliases are `timer_start` and `timer_cancel`.
- **Start:** `text` is optional (`TIMER` when empty), `duration_s` is required (1 to 43200), and `countdown` (once-a-minute reminders) is optional.
- **Refused:** an invalid text, a text already in use, a ninth timer, an unknown id and unknown fields.

## Tests

`timer_test.go` drives the timers on the simulated display (`display_message_test.go`). It covers:
- the reminder text (`4 MIN`, `4 MIN LEFT PATES` with several timers) and the alarm text (`TIME'S UP PATES`);
- parameter validation and reserved ids;
- an alarm looping without blanks, stopped by a key in standby, with the display handed back;
- a key held before the alarm, which does not count;
- the 10-minute limit;
- reminders at 3, 2 and 1 minute left, then the alarm, and the timer named in its reminders when several run;
- a duplicate text refused, cancel, and `display_cancel` leaving timers alone;
- two alarms taking turns;
- the limit of 8 timers.

`tests/webremote-ui.mjs` covers the panel, in standby and at 320 px.
