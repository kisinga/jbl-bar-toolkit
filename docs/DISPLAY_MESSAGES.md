# Display messages (Bar 9.1)

**Experimental.** The web remote can show your own text on the bar's front display, for example `FRIGO OUVERT` from a Home Assistant automation. It works with the bar on and in standby, without waking it. Nothing is stored: a message ends if the remote or the bar restarts.

Qualified on one Bar 9.1 (firmware 23.06.41.80) on 8 November 2026, first with a shell prototype, then through the remote. The details are in [`research/bar91-display.md`](../research/bar91-display.md#memory-write-route-qualified).

## What it does

- **Text.** 1–31 characters: letters, digits, space and `- . : ! ? '`. The display has no lower case or accents, so the remote upper-cases and folds the text (`frigo fermé` → `FRIGO FERME`) and turns line breaks into spaces.
- **How long.** Choose one of these; the default is one pass:
  - a number of passes, 1–99;
  - a duration, 1 s to 24 h. A pass that has started is shown to its end, so a message is never cut after its first word;
  - forever: until it is cancelled or replaced.
- **One pass** lasts as long as the bar shows its own temporary messages: length × 550 ms, or 4.95 s for fewer than six characters. A 1.5 s blank follows each pass. After the last pass, the blank appears only when the bar is on: it keeps the source name (`HDMI IN`) from reading as part of the message. In standby the screen goes black at once.
- **The bar comes first.** If the bar is showing something of its own, such as the volume, a pairing message, `HELLO` on wake-up or `SMART MODE OFF`, the message waits until the display has been free for one second. If that happens during a pass, the pass is shown again: a pass only counts when it was shown to its end. With a number of passes, each wait gives up after 60 s.
- **Ids.** Each message has an id (`default` when none is given):
  - a new message with the same id replaces the old one, whether it is showing or waiting, and takes its place in line;
  - messages with different ids are shown one after the other, never together;
  - a long or endless message lets a waiting message through between passes, then resumes.
- **Cancel** one id, or every message.

## API

`GET /api/features` lists the `bar91.display-message` extension. The usual LAN, same-origin and CSRF protections apply. Both actions return at once; the message is shown in the background.

```json
{"action":"bar91.display-message.show","parameters":{"text":"Frigo ouvert","id":"fridge","forever":true}}
{"action":"bar91.display-message.show","parameters":{"text":"Frigo fermé","id":"fridge","repeat":1}}
{"action":"bar91.display-message.show","parameters":{"text":"Colis livré","duration_s":300}}
{"action":"bar91.display-message.cancel","parameters":{"id":"fridge"}}
{"action":"bar91.display-message.cancel"}
```

`display_message` and `display_cancel` are short aliases. `repeat`, `duration_s` and `forever` are mutually exclusive. An `id` is 1–32 characters from `a-z 0-9 _ -`. The remote rejects invalid text, ids, counts, durations and unknown fields.

## How it works

bdpprog shows its own temporary text (native command `0x5b`) with three writes, and the remote makes the same three:

| Field | Address | Written |
| --- | --- | --- |
| Text | `0xd8a120` (display context + `0x98`) | 32 bytes, NUL-padded |
| Start tick | `0xd8a114` (context + `0x8c`) | current tick |
| Level-2 selector | `0xc654b0` (display state + `0x1c`) | `1`, last |

The renderer keeps the text while `(tick − start) × ms_per_tick < length × 550 ms`. It then selects nothing (`117`) again. The tick is `CLOCK_MONOTONIC` divided by `ms_per_tick` (`0xcf1854`, `1000 / CLK_TCK`, which is 10 ms on the bar). The comparison is unsigned, so a wrong tick ends the message at once instead of leaving it on screen. The remote refreshes the start tick every 200 ms, which lets it decide when a pass ends. Before writing, it checks that the bar's last tick is not ahead of its own clock.

**Standby.** Level 3 is drawn before level 2. In standby it holds `28`, a blank screen that the display module sets itself about 1.7 s after `STDBY`; no code outside the display module reads or writes it. While a message shows in standby, the remote sets level 3 to `52` (none), then puts `28` back. If the bar wakes up in the meantime, level 3 becomes the bar's again and is left alone.

**Limits.** Writes are restricted to these four fields (`display_native.go`). Each step takes the shared action gate, unreported, like the other monitors. When a control holds the gate, the step waits for the next poll. Handing the display back retries for 5 s and then writes without the gate, so a message never outlives its request.

## Tests

`display_message_test.go` drives the manager against a simulated display state and a fast clock. It covers:
- passes, with blanks between them and at the end;
- standby: the blank screen is lifted, then restored;
- waiting for a native message;
- a wake-up during a pass (`HELLO`, then `SMART MODE OFF`), after which the interrupted pass is shown again;
- replacement by id, cancellation, and the queue (an endless message lets a queued one through);
- a duration that ends after the last pass has finished;
- a busy gate during hand-back;
- a clock mismatch;
- the write whitelist, text folding and parameter validation.
