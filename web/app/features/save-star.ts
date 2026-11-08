import { busyDelay } from "../../shared/lib/busy-delay";

// Save and Star buttons: form[data-toggle] posts to /articles/{id}/save,
// /unsave, /star or /unstar. Post it with fetch instead, then flip
// aria-pressed (rows.css swaps the icon) and the action, so the page
// doesn't reload. Success is a 204; on anything else, including a
// redirect to the login page, submit the form normally so the round trip
// shows what happened. Delegated, so rows added by infinite scroll
// work too.
// While the request runs the button is aria-disabled (not disabled, so
// focus stays on it) and further presses are ignored; data-busy dims it
// only once the request has taken --delay-busy, so a quick one never
// flickers. A 204 that turns the button on sets data-just-toggled, which
// rows.css animates (the star's pop and burst, the bookmark's drop);
// nothing depends on it, and it's cleared once the animations end (with
// reduced motion none run, so it stays until the button is turned off).
export function init(): void {
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
    try {
      const res = await fetch(form.action, {
        method: "POST",
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
    button.setAttribute("aria-pressed", String(pressed));
    form.setAttribute(
      "action",
      form
        .getAttribute("action")!
        .replace(/\/(un)?(save|star)$/, (_: string, un: string | undefined, verb: string) =>
          pressed ? `/un${verb}` : `/${verb}`,
        ),
    );
    delete button.dataset.justToggled;
    if (pressed) {
      void button.offsetWidth; // restart the animation
      button.dataset.justToggled = "";
    }
  });
  document.addEventListener("animationend", (e) => {
    const button = e.target instanceof Element && e.target.closest<HTMLElement>("button[data-just-toggled]");
    if (!button || button.getAnimations({ subtree: true }).some((a) => a.playState === "running")) return;
    delete button.dataset.justToggled;
  });
}
