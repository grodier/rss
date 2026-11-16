// Recent searches: Discover's last few queries, kept in this browser's
// localStorage only and never sent to the server (see "Discover's search" in
// docs/features.md). A page of results records its query, which the server
// puts on the results container as data-recent-query. While
// form[data-live-search]'s q is focused and empty, the list, cloned from
// <template data-recent-template>, takes the place of [data-search-results]'
// contents; they come back on a press outside the form and the list, on
// focus moving outside them, on Escape, or when the reader types (live search
// then swaps in its own). Not on blur: it fires before a tap on a recent
// search lands, and Safari doesn't focus links on click. Clear empties the
// list. Storage that's blocked or throws just means no recent searches.

const storageKey = "paperround.recentSearches";
const limit = 5;

// load returns the stored queries, newest first; [] when there are none or
// storage can't be read.
function load(): string[] {
  try {
    const list: unknown = JSON.parse(localStorage.getItem(storageKey) ?? "[]");
    if (!Array.isArray(list)) return [];
    return list.filter((q): q is string => typeof q === "string" && q !== "").slice(0, limit);
  } catch {
    return [];
  }
}

function store(list: string[]): void {
  try {
    localStorage.setItem(storageKey, JSON.stringify(list));
  } catch {
    // Blocked or full: nothing is kept.
  }
}

// record puts q first, dropping an earlier copy of it (ignoring case), and
// keeps the newest 5.
function record(q: string): void {
  q = q.trim();
  if (q === "") return;
  const lower = q.toLowerCase();
  store([q, ...load().filter((old) => old.toLowerCase() !== lower)].slice(0, limit));
}

// clearRecentSearches forgets every recent search. The logged-out pages call
// it, so the next person on a shared device doesn't see them.
export function clearRecentSearches(): void {
  try {
    localStorage.removeItem(storageKey);
  } catch {
    // Blocked: there's nothing kept to clear.
  }
}

export function init(): void {
  const results = document.querySelector<HTMLElement>("[data-search-results]");
  if (!results) return;
  const query = results.getAttribute("data-recent-query");
  if (query !== null) record(query);

  const form = document.querySelector<HTMLFormElement>("form[data-live-search]");
  const input = form && form.elements.namedItem("q");
  const template = document.querySelector<HTMLTemplateElement>("template[data-recent-template]");
  if (!form || !(input instanceof HTMLInputElement) || !template) return;

  // While the list is shown: the list, and the container's contents it
  // replaced.
  let shown: Element | null = null;
  let saved: Node[] = [];

  const searchURL = (q: string) => {
    const u = new URL(form.action);
    u.search = new URLSearchParams({ q, scope: "top" }).toString();
    return u.pathname + u.search;
  };

  const render = (list: string[]): Element | null => {
    const section = (template.content.cloneNode(true) as DocumentFragment).firstElementChild;
    const row = section?.querySelector("li");
    if (!section || !row) return null;
    for (const q of list) {
      const li = row.cloneNode(true) as HTMLElement;
      li.querySelector("a")?.setAttribute("href", searchURL(q));
      const text = li.querySelector("[data-recent-text]");
      if (text) text.textContent = q;
      row.before(li);
    }
    row.remove();
    return section;
  };

  const show = () => {
    if (shown || input.value.trim() !== "") return;
    const list = load();
    if (list.length === 0) return;
    const section = render(list);
    if (!section) return;
    saved = [...results.childNodes];
    results.replaceChildren(section);
    shown = section;
  };

  // restore puts the container's contents back, unless something else (live
  // search) has replaced the list meanwhile.
  const restore = () => {
    if (!shown) return;
    if (shown.parentNode === results) results.replaceChildren(...saved);
    shown = null;
    saved = [];
  };

  const outside = (target: EventTarget | null) =>
    !(target instanceof Node) || !(form.contains(target) || results.contains(target));

  input.addEventListener("focus", show);
  input.addEventListener("input", restore);
  document.addEventListener("pointerdown", (e) => {
    if (outside(e.target)) restore();
  });
  document.addEventListener("focusin", (e) => {
    if (outside(e.target)) restore();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") restore();
  });
  results.addEventListener("click", (e) => {
    if (!shown || !(e.target instanceof Element) || !e.target.closest("[data-recent-clear]")) return;
    clearRecentSearches();
    restore();
    input.focus();
  });
}
