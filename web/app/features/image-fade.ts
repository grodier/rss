// Image fades: a card or article image still loading when this sees it
// waits unseen (data-image="loading") and fades in once it loads
// (data-image="loaded"); the styles are in rows.css and article.css. One
// already loaded, like a cached one, shows at once and never fades. Rows
// added later (infinite scroll, Home's swap) are marked as they arrive.
// Set up after image-fallback: an image that failed and is trying its
// fallback keeps waiting, and one that failed for good is removed, so
// nothing stays invisible.
import { IMAGES } from "./image-fallback";

// Design checkpoint switch (#525): "fade", an opacity fade, or "develop", a
// top-down reveal. Removed, with the other option's styles, once one is
// picked.
const STYLE: "fade" | "develop" = "fade";

export function init(): void {
  document.documentElement.dataset.imageFade = STYLE;
  const settle = (e: Event) => {
    const img = e.target;
    if (!(img instanceof HTMLImageElement) || img.dataset.image !== "loading") return;
    // image-fallback swapped in another source, which is loading now.
    if (e.type === "error" && !img.complete) return;
    if (e.type === "load") img.dataset.image = "loaded";
    else delete img.dataset.image;
  };
  document.addEventListener("load", settle, true);
  document.addEventListener("error", settle, true);

  const mark = (img: HTMLImageElement) => {
    if (!img.complete) img.dataset.image = "loading";
  };
  for (const img of document.querySelectorAll<HTMLImageElement>(IMAGES)) mark(img);
  new MutationObserver((records) => {
    for (const r of records) {
      for (const n of r.addedNodes) {
        if (!(n instanceof Element)) continue;
        if (n instanceof HTMLImageElement && n.matches(IMAGES)) mark(n);
        for (const img of n.querySelectorAll<HTMLImageElement>(IMAGES)) mark(img);
      }
    }
  }).observe(document.body, { childList: true, subtree: true });
}
