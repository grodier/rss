import { announce } from "./announce";
import { busyDelay } from "./busy-delay";

// Busy forms: form[data-busy] is a slow form (Look up, Refresh, Import,
// Log in, Sign up). Its post goes ahead as normal; on submit its button
// is aria-disabled (not disabled, which drops focus and leaves the button
// out of the form's data) and data-busy, its [data-busy-text] reads its
// data-busy-label, and further submits of the form are ignored. Once the
// post has taken --delay-busy a spinner (.btn-spinner) appears and the
// label is announced, since a changed label isn't read out by itself. A
// page restored from the back/forward cache (Back after Log in) gets its
// buttons back.
const busyText = new WeakMap<HTMLElement, string>();
const busyTimers = new WeakMap<HTMLFormElement, number>();

export function init(): void {
  document.addEventListener("submit", (e) => {
    const form = e.target;
    if (e.defaultPrevented || !(form instanceof HTMLFormElement) || !form.matches("form[data-busy]")) return;
    if (form.dataset.busyActive !== undefined) {
      e.preventDefault();
      return;
    }
    form.dataset.busyActive = "";
    const button = e.submitter || form.querySelector<HTMLElement>("button[type=submit], button:not([type])");
    if (!button) return;
    const text = button.querySelector("[data-busy-text]");
    const label = button.dataset.busyLabel;
    button.style.minInlineSize = `${button.getBoundingClientRect().width}px`;
    button.setAttribute("aria-disabled", "true");
    button.dataset.busy = "";
    if (text && label) {
      busyText.set(button, text.textContent);
      text.textContent = label;
    }
    const timer = setTimeout(() => {
      const spinner = document.createElement("span");
      spinner.className = "spinner btn-spinner";
      spinner.setAttribute("aria-hidden", "true");
      if (text) text.before(spinner);
      else button.prepend(spinner);
      if (label) announce(label);
    }, busyDelay());
    busyTimers.set(form, timer);
  });
  addEventListener("pageshow", (e) => {
    if (!e.persisted) return;
    for (const form of document.querySelectorAll<HTMLFormElement>("form[data-busy][data-busy-active]")) {
      clearTimeout(busyTimers.get(form));
      delete form.dataset.busyActive;
      for (const button of form.querySelectorAll<HTMLElement>("button[data-busy]")) {
        const text = button.querySelector("[data-busy-text]");
        if (text && busyText.has(button)) text.textContent = busyText.get(button) ?? "";
        for (const spinner of button.querySelectorAll(".btn-spinner")) spinner.remove();
        button.removeAttribute("aria-disabled");
        delete button.dataset.busy;
        button.style.minInlineSize = "";
      }
    }
  });
}
