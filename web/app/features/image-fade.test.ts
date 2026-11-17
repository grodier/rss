import { describe, expect, it } from "vitest";
import { isolate } from "../../shared/test/setup";

isolate();

// start sets up image-fallback then image-fade, as the entries do.
const start = async () => {
  (await import("./image-fallback")).init();
  (await import("./image-fade")).init();
};
// loading gives img the state of an image still loading, which happy-dom
// doesn't model: its images are always complete.
const loading = (img: HTMLImageElement, yes = true) =>
  Object.defineProperty(img, "complete", { value: !yes, configurable: true });
const img = () => document.querySelector<HTMLImageElement>("img")!;
const tick = () => new Promise((r) => setTimeout(r));

describe("image-fade", () => {
  it("fades in an image that was still loading", async () => {
    document.body.innerHTML = `<img class="article-image" src="https://a/x.jpg" alt="">`;
    loading(img());
    await start();
    expect(img().dataset.image).toBe("loading");

    loading(img(), false);
    img().dispatchEvent(new Event("load"));
    expect(img().dataset.image).toBe("loaded");
  });

  it("never fades an image already loaded", async () => {
    document.body.innerHTML = `<img class="reader-image" src="https://a/x.jpg" alt="">`;
    await start();
    expect(img().hasAttribute("data-image")).toBe(false);
  });

  it("marks images added later", async () => {
    await start();
    const row = document.createElement("div");
    row.innerHTML = `<div class="article-body"><p><img src="https://a/x.jpg"></p></div>`;
    loading(row.querySelector("img")!);
    document.body.append(row);
    await tick();
    expect(img().dataset.image).toBe("loading");
  });

  it("keeps waiting while the fallback loads, then fades it in", async () => {
    document.body.innerHTML = `<p><img class="article-image" src="https://a/feed.jpg" data-fallback-src="https://a/page.jpg" alt=""></p>`;
    loading(img());
    await start();

    img().dispatchEvent(new Event("error")); // still loading: the fallback
    expect(img().getAttribute("src")).toBe("https://a/page.jpg");
    expect(img().dataset.image).toBe("loading");

    loading(img(), false);
    img().dispatchEvent(new Event("load"));
    expect(img().dataset.image).toBe("loaded");
  });

  it("stops waiting for an image that failed for good", async () => {
    document.body.innerHTML = `<img class="article-image" src="https://a/x.jpg" alt="">`;
    const el = img();
    loading(el);
    (await import("./image-fade")).init(); // without image-fallback, it stays
    loading(el, false);
    el.dispatchEvent(new Event("error"));
    expect(el.hasAttribute("data-image")).toBe(false);
  });
});
