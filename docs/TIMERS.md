# Timers (Bar 9.1)

**Experimental.** The web remote can use the bar as a kitchen timer. When the time is up, the timer's text shows on the front display until you press any key on the JBL remote. It works with the bar on and in standby, without waking it. Timers live in the remote: a restart of the remote or the bar clears them.

Qualified on one Bar 9.1 (firmware 23.06.41.80) on 10 November 2026 with `vfdtest.sh`, a shell prototype. The Go implementation is covered by tests on a simulated display.

## In the page

The **Timers** panel has:

- a text, `TIMER!` by default. As for display messages, it is upper-cased and accents are folded;
- a duration in minutes, from 1 to 720, with 1, 5, 10 and 15 shortcuts;
- **Show the time left on the bar**, a countdown such as `PATES 4'59`. The name is left out when it would not fit in the 20 characters the display shows without scrolling.

Running timers are listed with their time left and a Cancel button. A ringing timer offers Stop. The panel works with the bar in standby.

## Rules

- **At most 8 timers.** Two timers cannot share a text, because the display would not tell them apart; the page says so.
- **Countdown.** The text is rewritten in place each second: a short text does not scroll or restart. It shows without blanks. When other messages wait, it lets them through every 10 seconds, like any long message.
- **When a timer ends**, its text shows like a message, with a 1.5 s blank between passes, until one of these:
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
- **Start:** `text` is optional (`TIMER!` when empty), `duration_s` is required (1 to 43200), and `countdown` is optional.
- **Refused:** an invalid text, a text already in use, a ninth timer, an unknown id and unknown fields.

## Tests

`timer_test.go` drives the timers on the simulated display (`display_message_test.go`). It covers:
- the clock format (`4'59`, `1H04'59`) and the name left out when it does not fit;
- parameter validation and reserved ids;
- an alarm stopped by a key in standby, with the display handed back;
- a key held before the alarm, which does not count;
- the 10-minute limit;
- the countdown rewritten in place without blanks, then replaced by the alarm;
- a duplicate text refused, cancel, and `display_cancel` leaving timers alone;
- two alarms taking turns;
- the limit of 8 timers.

`tests/webremote-ui.mjs` covers the panel, in standby and at 320 px.
