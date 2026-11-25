import { announce } from "../../shared/lib/announce";
import * as copied from "./copied";

// Copy buttons beside read-only links (.copy-field), on a shared timeline's
// page. Each button[data-copy] names its input by id and is rendered
// hidden, since it needs JavaScript; this shows it where the Clipboard API
// works (it needs a secure context) and its input exists. On click it
// copies the input's value and shows the outcome with copied.ts's
// showCopied, announced in the page's polite live region, named by the
// input's label ("Feed link copied"). When the copy fails, the input is
// focused with its text selected, so the reader can copy it themselves.
// Nothing about copying is recorded.
//
// With or without the buttons, a .copy-field's read-only input selects its
// whole value when it takes focus, by keyboard or by a press; a press that
// drags out part of it keeps that part, and later presses place the caret
// as usual.
export function init(): void {
  for (const input of document.querySelectorAll<HTMLInputElement>(".copy-field input[readonly]")) {
    selectOnFocus(input);
  }
  if (!navigator.clipboard?.writeText) return;
  const buttons = document.querySelectorAll<HTMLButtonElement>("button[data-copy]");
  if (buttons.length === 0) return;
  copied.init();
  for (const button of buttons) {
    const input = document.getElementById(button.dataset.copy ?? "");
    if (!(input instanceof HTMLInputElement)) continue;
    const done = `${input.labels?.[0]?.textContent?.trim() || "Link"} copied`;
    button.hidden = false;
    button.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(input.value);
      } catch {
        input.focus();
        input.select();
        copied.showCopied(button, false, done);
        announce("Couldn't copy the link. It's selected, so you can copy it.");
        return;
      }
      copied.showCopied(button, true, done);
      announce(done);
    });
  }
}

// selectOnFocus selects the input's value when it takes focus. A press
// places the caret after focus does, so a press that focused the input
// selects it again on click, unless it dragged out a selection of its own.
function selectOnFocus(input: HTMLInputElement): void {
  let pressedToFocus = false;
  input.addEventListener("pointerdown", () => {
    pressedToFocus = document.activeElement !== input;
  });
  input.addEventListener("focus", () => input.select());
  input.addEventListener("click", () => {
    if (pressedToFocus && input.selectionStart === input.selectionEnd) input.select();
    pressedToFocus = false;
  });
}
