// Password toggle: a Show/Hide button inside every password input. Before
// its form submits, revealed inputs go back to type=password so browsers
// still offer to save the password and don't keep it in autofill history.
export function init(): void {
  for (const input of document.querySelectorAll<HTMLInputElement>("input[type=password]")) {
    const wrap = document.createElement("div");
    wrap.className = "password-input";
    input.replaceWith(wrap);
    wrap.append(input);

    const button = document.createElement("button");
    button.type = "button";
    button.className = "password-toggle";
    button.textContent = "Show";
    button.setAttribute("aria-label", "Show password");
    button.setAttribute("aria-pressed", "false");
    button.setAttribute("aria-controls", input.id);
    wrap.append(button);

    const show = (on: boolean) => {
      input.type = on ? "text" : "password";
      button.textContent = on ? "Hide" : "Show";
      button.setAttribute("aria-pressed", String(on));
    };
    button.addEventListener("click", () => show(input.type === "password"));
    if (input.form) input.form.addEventListener("submit", () => show(false));
  }
}
