import { announce } from "../../shared/lib/announce";

// Share buttons: button[data-share-url] (the share-button partial) is
// rendered hidden, since it needs JavaScript; show it where it can work
// (a share sheet or the Clipboard API, which needs a secure context). On
// click, open the share sheet with data-share-title and the URL where there
// is one, else copy the URL, show the outcome with showCopied and announce
// it, in the page's polite live region (announce, also used by live
// search). enhanceShare takes a root so rows added to the page later can be
// enhanced too; the click is delegated.
let canShare: boolean | undefined;

export function enhanceShare(root: ParentNode): void {
  canShare ??= Boolean(navigator.share || navigator.clipboard?.writeText);
  if (!canShare) return;
  for (const b of root.querySelectorAll<HTMLButtonElement>("button[data-share-url]")) b.hidden = false;
}

// showCopied shows a copy's outcome on its button: its .copied-label
// (aria-hidden, since announce says it) reads "Link copied" or "Couldn't
// copy", shown by data-copied (rows.css also swaps the icon for a check
// that draws itself) or data-copy-failed. Only the latest copy shows, since
// the clipboard holds one link: a copy hides any other button's label at
// once. A success shows for 2 seconds, and longer while a mouse is over
// the button (until it leaves) or it has keyboard focus (until the next key
// or focus moves); a failure stays until the reader's next press or key,
// so it never times out before it's read. A success repeated while it
// shows replays its entrance without hiding and starts its time over:
// data-copy-round alternates "a" and "b", which rows.css uses to restart
// the animations, and data-copy-dip, set for an instant, dips the label
// under reduced motion (reading its style applies the dip before it goes,
// so the label fades back). The label keeps its text when it hides, so it fades out
// with it. lastPointer is the type of the latest press, since a touch
// leaves :hover on what it tapped, even on a device that also has a mouse.
let lastPointer = "";
let showing: HTMLElement | undefined; // the button whose label is showing
// The showing label's controls, by button: stop its timer and listeners,
// or hide it.
const copied = new WeakMap<HTMLElement, { stop: () => void; hide: () => void }>();

function showCopied(button: HTMLElement, ok: boolean): void {
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
    if (label) label.textContent = ok ? "Link copied" : "Couldn't copy";
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

export function init(): void {
  enhanceShare(document);
  document.addEventListener(
    "pointerdown",
    (e) => {
      lastPointer = e.pointerType;
    },
    { capture: true },
  );
  document.addEventListener("click", async (e) => {
    const button = e.target instanceof Element && e.target.closest<HTMLElement>("button[data-share-url]");
    if (!button) return;
    const url = button.dataset.shareUrl!;
    if (navigator.share) {
      try {
        await navigator.share({ title: button.dataset.shareTitle, url });
        return;
      } catch (err) {
        if ((err as Error).name === "AbortError") return; // the reader closed the sheet
      }
    }
    try {
      await navigator.clipboard.writeText(url);
    } catch {
      showCopied(button, false);
      announce("Couldn't copy the link");
      return;
    }
    showCopied(button, true);
    announce("Link copied");
  });
}
