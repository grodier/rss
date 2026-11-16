import { localizeTimes } from "../../shared/lib/local-times";
import { pollSelf } from "../../shared/lib/poll-self";
import { enhanceDivider } from "./divider-draw";
import * as infiniteScroll from "./infinite-scroll";
import { enhancePings } from "./read-pings";
import * as seenBeacon from "./seen-beacon";
import { enhanceShare } from "./share";

// INTERVAL is the wait between checks, and MAX_POLLS how many make about a
// minute: then the page is left as it is, and a reload checks again.
export const INTERVAL = 3000; // ms
export const MAX_POLLS = 20;

// Home while the feeds the reader follows have no articles yet
// ([data-home-fetching], "Getting the latest…"): check the page again every
// INTERVAL while it's visible, and when it has rows, swap them in for the
// message, with the pager or the end line after them, and set up what the
// reading entry sets up on rows (the list-wide modules found no list at
// load, so they set up now). Focus never moves. Without JavaScript, a meta
// refresh in <noscript> reloads the page instead.
export function init(): void {
  const waiting = document.querySelector("[data-home-fetching]");
  if (!waiting) return;
  pollSelf({
    interval: INTERVAL,
    maxPolls: MAX_POLLS,
    onPage: (doc) => {
      const list = doc.querySelector("main .article-list");
      if (!list) return false;
      const after = doc.querySelector("main .pager, main .timeline-end");
      waiting.replaceWith(...(after ? [list, after] : [list]));
      localizeTimes(list);
      enhancePings(list);
      enhanceShare(list);
      for (const d of list.querySelectorAll<HTMLElement>(".timeline-divider")) enhanceDivider(d);
      seenBeacon.init();
      infiniteScroll.init();
      return true;
    },
  });
}
