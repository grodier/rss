// Broken images: a card or article image that fails to load (a 404, or a
// host without a working https, which the CSP's img-src needs) tries its
// data-fallback-src (the article page's image, rss.CardImageFallbackURL),
// then is removed, so the row or article reads as one without an image
// instead of showing the browser's broken-image box. Without JavaScript the
// box stays, as before. Rows added later need nothing: error is listened
// for on document, in the capture phase, since it doesn't bubble.

// The images covered: publishers' pictures, not avatars or site icons,
// which we serve.
const IMAGES = ".article-image, .reader-image, .article-body img";

export function init(): void {
  document.addEventListener(
    "error",
    (e) => {
      if (e.target instanceof HTMLImageElement && e.target.matches(IMAGES)) failed(e.target);
    },
    true,
  );
  // An image can fail before this runs (the script is deferred): complete
  // but with no size. decode() tells it from a loaded image without one,
  // like an SVG with no width.
  for (const img of document.querySelectorAll<HTMLImageElement>(IMAGES)) {
    if (img.complete && img.naturalWidth === 0 && img.getAttribute("src")) {
      img.decode().catch(() => failed(img));
    }
  }
}

function failed(img: HTMLImageElement): void {
  if (!img.isConnected) return;
  const fallback = img.dataset.fallbackSrc;
  if (fallback) {
    delete img.dataset.fallbackSrc;
    img.src = fallback;
    return;
  }
  // In an article's body, what only held the image goes with it: its link,
  // its <picture>, its paragraph, or its figure and caption.
  let gone: Element = img;
  for (let p = img.parentElement; p && !p.matches(".article-body") && p.closest(".article-body"); p = p.parentElement) {
    if (!onlyHolds(p, gone)) break;
    gone = p;
  }
  gone.remove();
}

// onlyHolds reports whether parent has nothing to show but child: no other
// element (a figure's caption, a picture's sources and line breaks aside)
// and no text.
function onlyHolds(parent: Element, child: Element): boolean {
  for (const n of parent.childNodes) {
    if (n === child) continue;
    if (n instanceof Element) {
      if (n.localName === "source" || n.localName === "br") continue;
      if (n.localName === "figcaption" && parent.localName === "figure") continue;
      return false;
    }
    if (n.textContent?.trim()) return false;
  }
  return true;
}
