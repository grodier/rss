// Read pings: <a ping> records a click on the original article. Firefox
// disables ping by default, so send a beacon instead in every browser.
// Takes a root so rows added to the page later can be enhanced too.
export function enhancePings(root: ParentNode): void {
  for (const a of root.querySelectorAll("a[ping]")) {
    const url = a.getAttribute("ping")!;
    a.removeAttribute("ping");
    const send = () => navigator.sendBeacon(url);
    a.addEventListener("click", send);
    a.addEventListener("auxclick", (e) => {
      if ((e as MouseEvent).button === 1) send();
    });
  }
}
