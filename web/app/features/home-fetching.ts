import { announce } from "../../shared/lib/announce";
import { pollSelf } from "../../shared/lib/poll-self";
import { enhanceDivider } from "./divider-draw";
import * as infiniteScroll from "./infinite-scroll";
import * as seenBeacon from "./seen-beacon";

// INTERVAL is the wait between checks, and MAX_POLLS how many make about a
// minute.
export const INTERVAL = 3000; // ms
export const MAX_POLLS = 20;

// ARRIVED is what the polite live region says when the rows are swapped in,
// echoing the message's "Their newest articles will show up here".
export const ARRIVED = "Their newest articles are here.";

// Home while the feeds the reader follows were never fetched
// ([data-home-fetching], "Getting the latest…", its value Home's own
// address, since / shows whichever tab the tab cookie names): check that
// page every INTERVAL while it's visible, and when it has rows, swap them
// in for the message, with the pager or the end line after them, set them
// up as the reading entry does (the list-wide modules found no list at
// load, so they set up now) and announce them; focus never moves. After
// MAX_POLLS the spinner goes, since nothing is checking any more; a reload
// checks again. Without JavaScript, a meta refresh in <noscript> reloads
// the page instead.
export function init(): void {
  const waiting = document.querySelector<HTMLElement>("[data-home-fetching]");
  if (!waiting) return;
  pollSelf({
    url: waiting.dataset.homeFetching,
    interval: INTERVAL,
    maxPolls: MAX_POLLS,
    onGiveUp: () => waiting.querySelector(".spinner")?.remove(),
    onPage: (doc) => {
      const list = doc.querySelector("main .article-list");
      if (!list) return false;
      const after = doc.querySelector("main .pager, main .timeline-end");
      waiting.replaceWith(...(after ? [list, after] : [list]));
      infiniteScroll.enhanceRows(list);
      for (const d of list.querySelectorAll<HTMLElement>(".timeline-divider")) enhanceDivider(d);
      seenBeacon.init();
      infiniteScroll.init();
      announce(ARRIVED);
      return true;
    },
  });
}
