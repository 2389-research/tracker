# Width/Height Adaptive Fixes

Addresses three review findings about hardcoded widths, tiny terminal overflow,
and ANSI leakage in non-TTY output.

## 1. Width-adaptive TUI components

ChoiceModel and FreeformModel gain a `width` field (default 76) and
`SetWidth(w int)` method. `View()` passes `m.width` to `render.Prompt()`
instead of the hardcoded `76`. FreeformModel also adapts `textinput.Width`
to `width - 4` (border padding).

The dashboard AppModel computes modal content width as `termWidth - 6`
(DoubleBorder 2 chars + Padding(1,2) 4 chars), clamped to minimum 20. It
calls `SetWidth` on both modal models at terminal resize and modal creation.

## 2. NodeList tiny terminal graceful degradation

`visibleWindow()` handles small heights:

- `height < 2`: empty window, no nodes rendered
- `height == 2`: header + 1 node, no indicators
- `height >= 3`: current behavior with scroll indicators

`View()` renders just the "PIPELINE" header when the visible window is
empty but height > 0.

## 3. ConsoleInterviewer plain text rendering

Add `PromptPlain(prompt string, width int) string` to the `tui/render`
package. It word-wraps text to the given width without glamour/ANSI styling.

ConsoleInterviewer's `Ask()` and `AskFreeform()` use `PromptPlain()` instead
of `Prompt()` so piped/CI output stays free of escape sequences.
