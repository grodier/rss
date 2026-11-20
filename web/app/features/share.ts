import { announce } from "../../shared/lib/announce";
import * as copied from "./copied";

// Share buttons: button[data-share-url] (the share-button partial) is
// rendered hidden, since it needs JavaScript; show it where it can work
// (a share sheet or the Clipboard API, which needs a secure context). On
// click, open the share sheet with data-share-title and the URL where there
// is one, else copy the URL, show the outcome with copied.ts's showCopied and announce
// it, in the page's polite live region (announce, also used by live
// search). enhanceShare takes a root so rows added to the page later can be
// enhanced too; the click is delegated.
let canShare: boolean | undefined;

export function enhanceShare(root: ParentNode): void {
  canShare ??= Boolean(navigator.share || navigator.clipboard?.writeText);
  if (!canShare) return;
  for (const b of root.querySelectorAll<HTMLButtonElement>("button[data-share-url]")) b.hidden = false;
}

export function init(): void {
  enhanceShare(document);
  copied.init();
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
      copied.showCopied(button, false, "Link copied");
      announce("Couldn't copy the link");
      return;
    }
    copied.showCopied(button, true, "Link copied");
    announce("Link copied");
  });
}
