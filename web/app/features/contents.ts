// The article page's contents list (nav[data-contents]), shown beside the
// column on wide screens: marks the section being read with aria-current on
// its link, and shows the <h3>s of that section only (data-open on its
// top-level <li>; data-tracking on the nav lets article.css fold the
// others). The section being read is the last whose heading has passed
// READING_LINE of the way down the screen (or the one the URL points at,
// while it's near the top: see readingSection), or, once the page has been
// scrolled to its end, the last whose heading is on screen, so a short last
// section can still be marked. Above the first heading no section is. It
// follows the scroll at most once a frame, and only while the list is shown.
// Without it, the list shows every section's <h3>s and marks none.

// How far down the screen a heading has to pass to start its section.
const READING_LINE = 0.3;

// The width from which article.css shows the list.
const WIDE = "(min-width: 80rem)";

export function init(): void {
  const nav = document.querySelector<HTMLElement>("nav[data-contents]");
  if (!nav) return;
  const sections = [...nav.querySelectorAll<HTMLAnchorElement>('a[href^="#"]')].flatMap((link) => {
    const heading = document.getElementById(fragment(link.getAttribute("href")!));
    return heading ? [{ link, heading }] : [];
  });
  if (sections.length === 0) return;
  nav.dataset.tracking = "";

  const wide = matchMedia(WIDE);
  let current = -1;
  let queued = false;
  const update = () => {
    queued = false;
    if (!wide.matches) return;
    const height = window.innerHeight;
    // A page that fits the screen isn't at its end until it's scrolled.
    const atEnd = window.scrollY > 0 && height + window.scrollY >= document.documentElement.scrollHeight - 2;
    const tops = sections.map((s) => s.heading.getBoundingClientRect().top);
    const id = fragment(location.hash);
    const target = sections.findIndex((s) => s.heading.id === id);
    const next = readingSection(tops, height * READING_LINE, atEnd ? height : undefined, target);
    if (next === current) return;
    if (current >= 0) {
      sections[current].link.removeAttribute("aria-current");
      topItem(sections[current].link)?.removeAttribute("data-open");
    }
    current = next;
    if (current < 0) return;
    const { link } = sections[current];
    link.setAttribute("aria-current", "true");
    topItem(link)?.setAttribute("data-open", "");
    keepInView(nav, link);
  };
  const queue = () => {
    if (queued) return;
    queued = true;
    requestAnimationFrame(update);
  };
  window.addEventListener("scroll", queue, { passive: true });
  window.addEventListener("resize", queue);
  wide.addEventListener("change", queue);
  update();
}

// readingSection returns the index of the section being read, given its
// headings' tops (in document order, relative to the screen): the last at
// or above line, or, when bottom is given (the page is at its end), the last
// above bottom. It returns -1 above the first heading. A target (the index
// of the heading the page's URL points at) wins while its top is between
// the top of the screen and line: just after following a link to it, a
// short first subsection may have passed the line too, and the reader is
// reading the heading they asked for.
export function readingSection(tops: number[], line: number, bottom?: number, target = -1): number {
  if (target >= 0 && tops[target] >= -1 && tops[target] <= line) return target;
  const limit = bottom ?? line;
  let found = -1;
  tops.forEach((top, i) => {
    if (top <= limit) found = i;
  });
  return found;
}

// fragment returns the id a link's href, or the page's location.hash,
// points at, decoded as the page's ids are written ("#body-caf%C3%A9" is
// "body-café").
function fragment(href: string): string {
  const hash = href.slice(1);
  try {
    return decodeURIComponent(hash);
  } catch {
    return hash;
  }
}

// topItem returns the list's top-level <li> holding link: its own, or its
// <h2>'s for an <h3>.
function topItem(link: HTMLAnchorElement): HTMLElement | null {
  return link.closest<HTMLElement>(".reader-contents-list > li");
}

// keepInView scrolls nav, when its list is taller than the screen, so link
// is in it, a third of the way down.
function keepInView(nav: HTMLElement, link: HTMLAnchorElement): void {
  if (nav.scrollHeight <= nav.clientHeight) return;
  const box = nav.getBoundingClientRect();
  const { top, bottom } = link.getBoundingClientRect();
  if (top < box.top || bottom > box.bottom) nav.scrollTop += top - box.top - box.height / 3;
}
