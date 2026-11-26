import { announce } from "../../shared/lib/announce";

// My feeds' search, filtering in place: as the reader types in
// form[data-feeds-filter]'s q, hide the rows (li[data-filter]) whose text
// doesn't contain it, ignoring case, and the sections left with none, and
// show [data-feeds-none] when nothing matches. The server renders every row
// and hides the same ones for a ?q= (feedsHandler, which writes each row's
// lowercased data-filter), so this only toggles hidden and nothing is
// fetched. It moves the double rule that ends a site's group
// (data-after-group) to the next shown section. The URL, the sort and kind
// links and the no-match message's Discover link follow the query, keeping
// the rest of their own (the kind, the sort), so a reload, Back or a change
// of sort or kind keeps it. How many feeds match is announced once typing
// pauses; focus never moves. Enter filters as typing does, without a reload,
// and closes a phone's keyboard.
export function init(): void {
  const form = document.querySelector<HTMLFormElement>("form[data-feeds-filter]");
  const input = form && form.elements.namedItem("q");
  if (!form || !(input instanceof HTMLInputElement)) return;
  const rows = [...document.querySelectorAll<HTMLElement>("li[data-filter]")];
  const sections = [...new Set(rows.map((row) => row.closest("section")).filter((s) => s !== null))];
  const none = document.querySelector<HTMLElement>("[data-feeds-none]");
  let timer = 0;

  const withQ = (href: string, q: string) => {
    const u = new URL(href, location.href);
    if (q) u.searchParams.set("q", q);
    else u.searchParams.delete("q");
    return u.pathname + u.search;
  };

  const apply = () => {
    const q = input.value.trim();
    const needle = q.toLowerCase();
    let matches = 0;
    for (const row of rows) {
      row.hidden = !(row.dataset.filter ?? "").includes(needle);
      if (!row.hidden) matches++;
    }
    // A group ends in a double rule above the next shown section that
    // isn't a group (data-after-group), as feedsHandler marks it.
    let afterGroup = false;
    for (const section of sections) {
      section.hidden = !section.querySelector("li[data-filter]:not([hidden])");
      const grouped = section.hasAttribute("data-grouped");
      section.toggleAttribute("data-after-group", !section.hidden && afterGroup && !grouped);
      if (!section.hidden) afterGroup = grouped;
    }
    if (none) {
      none.hidden = matches > 0;
      const said = none.querySelector("[data-feeds-q]");
      if (said) said.textContent = q;
      const discover = none.querySelector<HTMLAnchorElement>("a[data-feeds-discover]");
      if (discover) discover.href = withQ(discover.href, q);
    }
    for (const link of document.querySelectorAll<HTMLAnchorElement>("a[data-sort-link], a[data-kind-link]")) {
      link.href = withQ(link.href, q);
    }
    history.replaceState(null, "", withQ(location.href, q));
    clearTimeout(timer);
    timer = setTimeout(() => {
      announce(matches === 0 ? "No feeds" : matches === 1 ? "1 feed" : `${matches} feeds`);
    }, 500);
  };

  input.addEventListener("input", apply);
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    apply();
    input.blur();
  });
}
