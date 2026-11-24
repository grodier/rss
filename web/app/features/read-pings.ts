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

// Read marks: following a row's title link (the article page records the
// read) or its source link (the ping above records it) marks the row read
// at once, as the list draws it on its next load, so a list that isn't
// loaded again (Back from the back/forward cache, a link opened in another
// tab) shows it too. Only those two links record a read, so only they mark
// the row; other copies of the article get their marker on the next load.
// Delegated on document, so rows added to the page later are covered too.
export function initReadMarks(): void {
  const mark = (e: Event) => {
    if (!(e.target instanceof Element)) return;
    const row = e.target
      .closest(".article-row .article-title a, .article-row a.article-source")
      ?.closest(".article-row");
    if (!row || row.classList.contains("read")) return;
    row.classList.add("read");
    // The same hidden text partials/article_row.html renders after a read
    // row's title link; keep the two in step (TestArticleRowReadMarkup).
    const hidden = document.createElement("span");
    hidden.className = "visually-hidden";
    hidden.textContent = " (read)";
    row.querySelector(".article-title")?.append(hidden);
  };
  document.addEventListener("click", mark);
  document.addEventListener("auxclick", (e) => {
    if (e.button === 1) mark(e);
  });
}
