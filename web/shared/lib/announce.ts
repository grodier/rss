// announce says msg in the page's polite live region, made the first time
// it's needed: a visually hidden paragraph at the end of the body.
let liveStatus: HTMLParagraphElement | undefined;

export function announce(msg: string): void {
  if (!liveStatus) {
    liveStatus = document.createElement("p");
    liveStatus.className = "visually-hidden";
    liveStatus.setAttribute("aria-live", "polite");
    document.body.append(liveStatus);
  }
  const status = liveStatus;
  // Clear it first so the same message is announced again.
  status.textContent = "";
  setTimeout(() => {
    status.textContent = msg;
  }, 50);
}
