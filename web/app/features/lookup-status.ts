import { announce } from "../../shared/lib/announce";
import { pollSelf } from "../../shared/lib/poll-self";

// Lookup status: while a lookup runs, its page ([data-lookup-poll]) polls
// itself with pollSelf. A redirect to the site page replaces this page, so
// Back skips the finished lookup; any other redirect (Log in) is followed.
// A 200 without [data-lookup-poll] (failed, or no feeds) swaps in its
// .empty section and announces its first line in the polite live region;
// focus never moves. Still in progress: the page is left alone, so the
// spinner keeps turning.
export function init(): void {
  let section = document.querySelector("[data-lookup-poll]");
  if (!section) return;
  pollSelf({
    onRedirect: (url) => {
      const to = new URL(url);
      if (to.origin === location.origin && to.pathname.startsWith("/sites/")) location.replace(to.href);
      else location.assign(to.href);
    },
    onPage: (doc) => {
      if (doc.querySelector("[data-lookup-poll]")) return false;
      const next = doc.querySelector("main .empty");
      if (!next) return false;
      section!.replaceWith(next);
      section = next;
      const line = next.querySelector("p");
      if (line) announce(line.textContent.trim());
      return true;
    },
  });
}
