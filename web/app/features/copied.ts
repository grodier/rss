// A copy's outcome on its button, shared by Share (share.ts) and the code
// blocks' Copy buttons (code-copy.ts), so copying looks the same
// everywhere. Call init once before the first showCopied.
//
// showCopied shows the outcome on the button's .copied-label (aria-hidden,
// since the caller announces it): text, like "Link copied", or "Couldn't
// copy", shown by data-copied or data-copy-failed (copied.css; rows.css also
// swaps Share's icon for a check that draws itself). Only the latest copy
// shows, since the clipboard holds one thing: a copy hides any other
// button's label at once. A success shows for 2 seconds, and longer while a
// mouse is over the button (until it leaves) or it has keyboard focus
// (until the next key or focus moves); a failure stays until the reader's
// next press or key, so it never times out before it's read. A success
// repeated while it shows replays its entrance without hiding and starts its
// time over: data-copy-round alternates "a" and "b", which the styles use to
// restart the animations, and data-copy-dip, set for an instant, dips the
// label under reduced motion (reading its style applies the dip before it
// goes, so the label fades back). The label keeps its text when it hides, so
// it fades out with it. lastPointer is the type of the latest press, since a
// touch leaves :hover on what it tapped, even on a device that also has a
// mouse.
let lastPointer = "";
let tracking = false;
let showing: HTMLElement | undefined; // the button whose label is showing
// The showing label's controls, by button: stop its timer and listeners,
// or hide it.
const copied = new WeakMap<HTMLElement, { stop: () => void; hide: () => void }>();

export function init(): void {
  if (tracking) return;
  tracking = true;
  document.addEventListener(
    "pointerdown",
    (e) => {
      lastPointer = e.pointerType;
    },
    { capture: true },
  );
}

export function showCopied(button: HTMLElement, ok: boolean, text: string): void {
  const label = button.querySelector(".copied-label");
  const again = ok && button === showing && "copied" in button.dataset;
  if (again) {
    copied.get(button)?.stop();
    button.dataset.copyRound = button.dataset.copyRound === "a" ? "b" : "a";
    button.dataset.copyDip = "";
    if (label) void getComputedStyle(label).opacity;
    delete button.dataset.copyDip;
  } else {
    if (showing) copied.get(showing)?.hide();
    if (label) label.textContent = ok ? text : "Couldn't copy";
    button.dataset[ok ? "copied" : "copyFailed"] = "";
  }
  showing = button;
  const done = new AbortController();
  const { signal } = done;
  let timer: number | undefined;
  const stop = () => {
    clearTimeout(timer);
    done.abort();
  };
  const hide = () => {
    stop();
    delete button.dataset.copied;
    delete button.dataset.copyFailed;
    delete button.dataset.copyRound;
    if (showing === button) showing = undefined;
  };
  copied.set(button, { stop, hide });
  if (!ok) {
    for (const type of ["pointerdown", "keydown"]) document.addEventListener(type, hide, { signal, capture: true });
    return;
  }
  const hovered = () => lastPointer === "mouse" && button.matches(":hover");
  const focused = () => button.matches(":focus-visible");
  timer = setTimeout(() => {
    if (hovered()) {
      button.addEventListener("pointerleave", hide, { signal });
    } else if (focused()) {
      button.addEventListener("blur", hide, { signal });
      document.addEventListener("keydown", hide, { signal, capture: true });
    } else {
      hide();
    }
  }, 2000);
}
