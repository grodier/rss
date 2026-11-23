import { announce } from "../../shared/lib/announce";

// Save, Star and Follow buttons: form[data-toggle] posts to
// /articles/{id}/save, /unsave, /star or /unstar, or /feeds/{id}/subscribe
// or /unsubscribe. Post it with fetch instead, and flip aria-pressed (the
// stylesheet swaps the icon or the label) and the action, so the page
// doesn't reload, and announce the form's data-announce-pressed or
// data-announce-unpressed, if it has one, in the polite live region.
// Success is a 204. fetch doesn't follow a redirect itself (redirect:
// "manual"), so on anything else, the redirect to the login page included,
// the form is submitted normally: the round trip shows what happened, and
// the page it leads to loads once, with its flash. Delegated, so rows added
// by infinite scroll work too.
//
// Save and Star answer the press at once, since both routes are
// idempotent: the button flips, announces and, turning on, sets
// data-just-toggled, which rows.css animates (the star's pop and burst, the
// bookmark's drop), and further presses count. The script keeps the state
// the last 204 confirmed and the one the reader last pressed, and sends one
// request at a time per button until they match, so the last press wins
// and the server sees a button's requests in order. On a failure the
// button goes back to the confirmed state, without an animation, and the
// form for the wanted state is submitted.
// Requests are keepalive, so one that's running still reaches the server
// if the reader leaves the page; a press waiting behind it is sent at once
// on pagehide, the one time a button's requests overlap, as the page won't
// be there to send it later.
//
// Follow waits for the server, since unfollowing a feed with a name or
// tags redirects to a confirmation. While its request runs the button is
// aria-disabled (not disabled, so focus stays on it) and further presses
// are ignored. Follow in a list for finding feeds (form[data-follow-once])
// isn't flipped: the hidden "✓ Following" link it holds takes the form's
// place, focused, as the list shows a followed feed after a reload.
//
// A button whose form is submitted stays aria-disabled while the page
// loads; a page restored from the back/forward cache, such as coming Back
// from Follow's confirmation, gets its buttons back (pageshow).
// data-just-toggled is cleared once the animations end (with reduced motion
// none run, so it stays until the button is turned off).
const instant = /\/(un)?(save|star)$/;
const toggles = /\/(un)?(save|star|subscribe)$/;

// actionTo returns the action of form that turns its button on or off:
// /save or /unsave, and so on.
function actionTo(form: HTMLFormElement, on: boolean): string {
  return form
    .getAttribute("action")!
    .replace(toggles, (_: string, _un: string | undefined, verb: string) => (on ? `/${verb}` : `/un${verb}`));
}

// setPressed shows the button on or off, with the action that flips it.
function setPressed(form: HTMLFormElement, button: HTMLButtonElement, on: boolean): void {
  button.setAttribute("aria-pressed", String(on));
  form.setAttribute("action", actionTo(form, !on));
}

function post(form: HTMLFormElement, action: string): Promise<Response> {
  return fetch(new URL(action, location.href).href, {
    method: "POST",
    redirect: "manual",
    keepalive: true,
    // FormData is a sequence of name and value pairs, which TypeScript's
    // types don't let URLSearchParams take.
    body: new URLSearchParams(new FormData(form) as unknown as string[][]),
  });
}

function announceState(form: HTMLFormElement, on: boolean): void {
  const message = on ? form.dataset.announcePressed : form.dataset.announceUnpressed;
  if (message) announce(message);
}

// A Save or Star button's state: what the last 204 confirmed, what the
// reader last pressed, and the state its running request asks for, if
// one is running.
type Toggle = {
  form: HTMLFormElement;
  button: HTMLButtonElement;
  confirmed: boolean;
  wanted: boolean;
  sending?: boolean;
};

export function init(): void {
  const states = new WeakMap<HTMLButtonElement, Toggle>();
  const running = new Set<Toggle>();

  // press answers a press on Save or Star, and sends its request unless
  // one is already running, which send's loop then follows with it.
  function press(form: HTMLFormElement, button: HTMLButtonElement): void {
    const on = button.getAttribute("aria-pressed") !== "true";
    let state = states.get(button);
    if (!state) {
      state = { form, button, confirmed: !on, wanted: !on };
      states.set(button, state);
    }
    state.wanted = on;
    setPressed(form, button, on);
    announceState(form, on);
    delete button.dataset.justToggled;
    if (on) {
      void button.offsetWidth; // restart the animation
      button.dataset.justToggled = "";
    }
    if (state.sending === undefined) void send(state);
  }

  async function send(state: Toggle): Promise<void> {
    const { form, button } = state;
    running.add(state);
    while (state.wanted !== state.confirmed) {
      const on = state.wanted;
      state.sending = on;
      try {
        const res = await post(form, actionTo(form, on));
        if (res.status !== 204) throw new Error(`status ${res.status}`);
      } catch {
        delete state.sending;
        running.delete(state);
        delete button.dataset.justToggled;
        setPressed(form, button, state.confirmed);
        // Submit the wanted state's form, even when the reader has pressed
        // back to the confirmed state: it's idempotent, and the page shows
        // what went wrong. form.submit() reads the action at once.
        const action = form.getAttribute("action")!;
        form.setAttribute("action", actionTo(form, state.wanted));
        button.setAttribute("aria-disabled", "true");
        form.submit();
        form.setAttribute("action", action);
        return;
      }
      state.confirmed = on;
    }
    delete state.sending;
    running.delete(state);
  }

  async function follow(form: HTMLFormElement, button: HTMLButtonElement): Promise<void> {
    button.setAttribute("aria-disabled", "true");
    try {
      const res = await post(form, form.getAttribute("action")!);
      if (res.status !== 204) throw new Error(`status ${res.status}`);
    } catch {
      form.submit();
      return;
    }
    button.removeAttribute("aria-disabled");
    const on = button.getAttribute("aria-pressed") !== "true";
    const label = form.matches("[data-follow-once]") && form.querySelector<HTMLElement>(".following-label");
    if (on && label) {
      label.hidden = false;
      form.replaceWith(label);
      label.focus({ preventScroll: true }); // where the button was, already in view
      announceState(form, on);
      return;
    }
    setPressed(form, button, on);
    announceState(form, on);
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
    }
  });
  addEventListener("pagehide", () => {
    for (const { form, wanted, sending } of running) {
      if (wanted !== sending) void post(form, actionTo(form, wanted)).catch(() => {});
    }
  });
  document.addEventListener("animationend", (e) => {
    const button = e.target instanceof Element && e.target.closest<HTMLElement>("button[data-just-toggled]");
    if (!button || button.getAnimations({ subtree: true }).some((a) => a.playState === "running")) return;
    delete button.dataset.justToggled;
  });
}
