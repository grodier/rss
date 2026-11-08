import { announce } from "../../shared/lib/announce";
import { pollSelf } from "../../shared/lib/poll-self";

// Import status: while an import runs, its page ([data-import-poll]) polls
// itself with pollSelf and inserts what's new without replacing anything
// already on the page, so focus, selection and the scroll position stay
// put. A row lands in one group ([data-import-group]) once and never
// moves, and rows are in position order, so data-position is a row's key
// and where to insert it. A missing group is inserted whole in its place
// in groupOrder. While it runs, the progress line changes silently; when it
// finishes, the status section, h1 and title change to the finished ones
// and only the finish is announced. Focus never moves. Inserted rows hold
// nothing the enhancements change; if that changes, run them through
// localizeTimes, enhancePings and enhanceShare, as live search does.
export function init(): void {
  const status = document.querySelector("[data-import-poll]");
  const main = status && status.closest("main");
  if (!status || !main) return;
  const groupOrder = ["subscribed", "already", "failed"];
  const groupOf = (el: HTMLElement) => groupOrder.indexOf(String(el.dataset.importGroup));
  const mergeGroup = (next: HTMLElement) => {
    const name = next.dataset.importGroup;
    const groups = [...main.querySelectorAll<HTMLElement>("[data-import-group]")];
    const current = groups.find((g) => g.dataset.importGroup === name);
    if (!current) {
      const after = groups.find((g) => groupOf(g) > groupOf(next));
      if (after) after.before(next);
      else main.append(next);
      return;
    }
    const list = current.querySelector("ul");
    if (!list) return;
    const rows = [...list.querySelectorAll<HTMLElement>(":scope > li[data-position]")];
    const have = new Set(rows.map((li) => Number(li.dataset.position)));
    for (const li of next.querySelectorAll<HTMLElement>("li[data-position]")) {
      const pos = Number(li.dataset.position);
      if (have.has(pos)) continue;
      const before = rows.find((r) => Number(r.dataset.position) > pos);
      if (before) before.before(li);
      else list.append(li);
      have.add(pos);
      rows.splice(before ? rows.indexOf(before) : rows.length, 0, li);
    }
  };
  pollSelf({
    onPage: (doc) => {
      for (const next of doc.querySelectorAll<HTMLElement>("[data-import-group]")) mergeGroup(next);
      if (doc.querySelector("[data-import-poll]")) {
        const progress = status.querySelector("[data-import-progress]");
        const nextProgress = doc.querySelector("[data-import-progress]");
        if (progress && nextProgress) progress.textContent = nextProgress.textContent;
        return false;
      }
      const nextStatus = doc.querySelector(".import-status");
      if (!nextStatus) return false;
      status.replaceWith(nextStatus);
      const h1 = main.querySelector("h1");
      const nextH1 = doc.querySelector("h1");
      if (h1 && nextH1) h1.textContent = nextH1.textContent;
      document.title = doc.title;
      const summary = nextStatus.querySelector("p");
      announce("Import finished. " + (summary ? summary.textContent.trim() : ""));
      return true;
    },
  });
}
