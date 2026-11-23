import { announce } from "../../shared/lib/announce";

// Save, Star and Follow buttons: form[data-toggle] posts to
// /articles/{id}/save, /unsave, /star or /unstar, or /feeds/{id}/subscribe
// or /unsubscribe. Post it with fetch instead, and flip aria-pressed (the
// stylesheet swaps the icon or the label) and the action, so the page
// doesn't reload, and announce the form's data-announce-pressed or
// data-announce-unpressed, if it has one, in the polite live region.
// Success is a 204; fetch doesn't follow a redirect itself (redirect:
// "manual"), so on anything else, the redirect to the login page included,
// the form is submitted normally, the round trip shows what happened, and
// the page it leads to loads once, with its flash. Delegated, so rows added
// by infinite scroll work too.
//
// Save and Star answer the press at once: both routes are idempotent, so
// the button flips, announces and, turning on, sets data-just-toggled,
// which rows.css animates (the star's pop and burst, the bookmark's drop),
// before the request goes. Further presses count. The script keeps the
// state the last 204 confirmed and the one the reader wants, and sends one
// request at a time per button: when a request ends and the reader now
// wants the other state, it sends that one; when they match, nothing more.
// So the reader's last press wins and the server sees a button's requests
// in order. On a failure the button goes back to the confirmed state, with
// no animation, and the form for the state the reader wants is submitted.
//
// Follow waits for the server, since unfollowing a feed with a name or
// tags redirects to a confirmation, and Follow in a list for finding feeds
// (form[data-follow-once]) isn't flipped: the hidden "✓ Following" link it
// holds takes the form's place, focused, as the list shows a followed feed
// after a reload. While its request runs the button is aria-disabled (not
// disabled, so focus stays on it) and further presses are ignored.
//
// A button whose form is being submitted stays aria-disabled while the
// page loads; a page restored from the back/forward cache after a submit,
// such as coming Back from the confirmation, gets its buttons back
// (pageshow). data-just-toggled is cleared once the animations end (with
// reduced motion none run, so it stays until the button is turned off).
const instant = /\/(un)?(save|star)$/;
const verbs = /\/(un)?(save|star|subscribe)$/;

// toActionFor returns the address of form's action that turns its button on
// (pressed) or off: /save or /unsave, and so on.
function toActionFor(form: HTMLFormElement, pressed: boolean): string {
  return form
    .getAttribute("action")!
    .replace(verbs, (_: string, _un: string | undefined, verb: string) => (pressed ? `/${verb}` : `/un${verb}`));
}

// show puts the button in a state: aria-pressed, and the action of the
// press that leaves it.
function show(form: HTMLFormElement, button: HTMLButtonElement, pressed: boolean): void {
  button.setAttribute("aria-pressed", String(pressed));
  form.setAttribute("action", toActionFor(form, !pressed));
}

function post(form: HTMLFormElement, action: string): Promise<Response> {
  return fetch(new URL(action, location.href).href, {
    method: "POST",
    redirect: "manual",
    // FormData is a sequence of name and value pairs, which TypeScript's
    // types don't let URLSearchParams take.
    body: new URLSearchParams(new FormData(form) as unknown as string[][]),
  });
}

// submitFor submits form normally, as the press that turns its button on
// (pressed) or off, and leaves the button aria-disabled while the page
// loads.
function submitFor(form: HTMLFormElement, button: HTMLButtonElement, pressed: boolean): void {
  button.setAttribute("aria-disabled", "true");
  const action = form.getAttribute("action")!;
  form.setAttribute("action", toActionFor(form, pressed));
  form.submit(); // reads the action now
  form.setAttribute("action", action);
}

function announceFor(form: HTMLFormElement, pressed: boolean): void {
  const message = pressed ? form.dataset.announcePressed : form.dataset.announceUnpressed;
  if (message) announce(message);
}

type Toggle = { confirmed: boolean; wanted: boolean; sending: boolean };

export function init(): void {
  const toggles = new WeakMap<HTMLButtonElement, Toggle>();

  // send posts the wanted state until it's the confirmed one, one request
  // at a time.
  async function send(form: HTMLFormElement, button: HTMLButtonElement, toggle: Toggle): Promise<void> {
    toggle.sending = true;
    while (toggle.wanted !== toggle.confirmed) {
      const pressed = toggle.wanted;
      try {
        const res = await post(form, toActionFor(form, pressed));
        if (res.status !== 204) throw new Error(`status ${res.status}`);
      } catch {
        toggle.sending = false;
        delete button.dataset.justToggled;
        show(form, button, toggle.confirmed);
        submitFor(form, button, toggle.wanted);
        return;
      }
      toggle.confirmed = pressed;
    }
    toggle.sending = false;
  }

  function press(form: HTMLFormElement, button: HTMLButtonElement): void {
    const pressed = button.getAttribute("aria-pressed") !== "true";
    let toggle = toggles.get(button);
    if (!toggle) {
      toggle = { confirmed: !pressed, wanted: !pressed, sending: false };
      toggles.set(button, toggle);
    }
    toggle.wanted = pressed;
    show(form, button, pressed);
    announceFor(form, pressed);
    delete button.dataset.justToggled;
    if (pressed) {
      void button.offsetWidth; // restart the animation
      button.dataset.justToggled = "";
    }
    if (!toggle.sending) void send(form, button, toggle);
  }

  async function follow(form: HTMLFormElement, button: HTMLButtonElement): Promise<void> {
    button.setAttribute("aria-disabled", "true");
    try {
      const res = await post(form, form.getAttribute("action")!);
      if (res.status !== 204) throw new Error(`status ${res.status}`);
    } catch {
      form.submit(); // stays aria-disabled while the page loads
      return;
    }
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
    show(form, button, pressed);
    announceFor(form, pressed);
  }

  document.addEventListener("submit", (e) => {
    const form = e.target;
    if (!(form instanceof HTMLFormElement) || !form.matches("form[data-toggle]")) return;
    e.preventDefault();
    const button = form.querySelector<HTMLButtonElement>("button[aria-pressed]");
    if (!button || button.getAttribute("aria-disabled") === "true") return;
    if (instant.test(form.getAttribute("action")!)) press(form, button);
    else void follow(form, button);
  });
  addEventListener("pageshow", (e) => {
    if (!e.persisted) return;
    for (const button of document.querySelectorAll<HTMLButtonElement>(
      'form[data-toggle] button[aria-disabled="true"]',
    )) {
      button.removeAttribute("aria-disabled");
      const toggle = toggles.get(button);
      if (toggle) toggle.wanted = toggle.confirmed;
    }
  });
  document.addEventListener("animationend", (e) => {
    const button = e.target instanceof Element && e.target.closest<HTMLElement>("button[data-just-toggled]");
    if (!button || button.getAnimations({ subtree: true }).some((a) => a.playState === "running")) return;
    delete button.dataset.justToggled;
  });
}
