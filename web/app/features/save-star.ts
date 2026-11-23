import { announce } from "../../shared/lib/announce";
import { busyDelay } from "../../shared/lib/busy-delay";

// Save, Star and Follow buttons: form[data-toggle] posts to
// /articles/{id}/save, /unsave, /star or /unstar, or /feeds/{id}/subscribe
// or /unsubscribe. Post it with fetch instead, then flip aria-pressed (the
// stylesheet swaps the icon or the label) and the action, so the page
// doesn't reload, and announce the form's data-announce-pressed or
// data-announce-unpressed, if it has one, in the polite live region.
// Follow in a list for finding feeds (form[data-follow-once]) isn't
// flipped: the hidden "✓ Following" link it holds takes the form's place,
// focused, as the list shows a followed feed after a reload.
// Success is a 204; on anything else, including a
// redirect (to the login page, or to the confirmation before unfollowing
// a feed with a name or tags), submit the form normally so the round trip
// shows what happened. fetch doesn't follow the redirect itself, so the
// page it leads to loads once, with its flash. Delegated, so rows added by
// infinite scroll work too.
// While the request runs the button is aria-disabled (not disabled, so
// focus stays on it) and further presses are ignored; data-busy dims it
// only once the request has taken --delay-busy, so a quick one never
// flickers. A 204 that turns the button on sets data-just-toggled, which
// rows.css animates (the star's pop and burst, the bookmark's drop);
// nothing depends on it, and it's cleared once the animations end (with
// reduced motion none run, so it stays until the button is turned off).
// A page restored from the back/forward cache after a submit, such as
// coming Back from the confirmation, gets its buttons back (pageshow).
export function init(): void {
  const busyTimers = new WeakMap<HTMLButtonElement, ReturnType<typeof setTimeout>>();
  document.addEventListener("submit", async (e) => {
    const form = e.target;
    if (!(form instanceof HTMLFormElement) || !form.matches("form[data-toggle]")) return;
    e.preventDefault();
    const button = form.querySelector<HTMLButtonElement>("button[aria-pressed]");
    if (!button || button.getAttribute("aria-disabled") === "true") return;
    button.setAttribute("aria-disabled", "true");
    const busyTimer = setTimeout(() => {
      button.dataset.busy = "";
    }, busyDelay());
    busyTimers.set(button, busyTimer);
    try {
      const res = await fetch(form.action, {
        method: "POST",
        redirect: "manual",
        // FormData is a sequence of name and value pairs, which TypeScript's
        // types don't let URLSearchParams take.
        body: new URLSearchParams(new FormData(form) as unknown as string[][]),
      });
      if (res.status !== 204) throw new Error(`status ${res.status}`);
    } catch {
      form.submit(); // stays busy while the page loads
      return;
    }
    clearTimeout(busyTimer);
    delete button.dataset.busy;
    button.removeAttribute("aria-disabled");
    const pressed = button.getAttribute("aria-pressed") !== "true";
    const label = form.matches("[data-follow-once]") && form.querySelector<HTMLElement>(".following-label");
    if (pressed && label) {
      label.hidden = false;
      form.replaceWith(label);
      label.focus({ preventScroll: true }); // where the button was, already in view
      if (form.dataset.announcePressed) announce(form.dataset.announcePressed);
      return;
    }
    button.setAttribute("aria-pressed", String(pressed));
    form.setAttribute(
      "action",
      form
        .getAttribute("action")!
        .replace(/\/(un)?(save|star|subscribe)$/, (_: string, un: string | undefined, verb: string) =>
          pressed ? `/un${verb}` : `/${verb}`,
        ),
    );
    const message = pressed ? form.dataset.announcePressed : form.dataset.announceUnpressed;
    if (message) announce(message);
    delete button.dataset.justToggled;
    if (pressed) {
      void button.offsetWidth; // restart the animation
      button.dataset.justToggled = "";
    }
  });
  addEventListener("pageshow", (e) => {
    if (!e.persisted) return;
    for (const button of document.querySelectorAll<HTMLButtonElement>(
      'form[data-toggle] button[aria-disabled="true"]',
    )) {
      clearTimeout(busyTimers.get(button));
      delete button.dataset.busy;
      button.removeAttribute("aria-disabled");
    }
  });
  document.addEventListener("animationend", (e) => {
    const button = e.target instanceof Element && e.target.closest<HTMLElement>("button[data-just-toggled]");
    if (!button || button.getAnimations({ subtree: true }).some((a) => a.playState === "running")) return;
    delete button.dataset.justToggled;
  });
}
