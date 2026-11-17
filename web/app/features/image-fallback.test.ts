import { describe, expect, it, vi } from "vitest";
import { isolate } from "../../shared/test/setup";

isolate();

const start = async () => (await import("./image-fallback")).init();
const fail = (img: Element) => img.dispatchEvent(new Event("error"));

// loaded gives img the state of a loaded image (ok) or a broken one, which
// happy-dom doesn't model: its images are always complete, with no size.
const loaded = (img: HTMLImageElement, ok: boolean) => {
  Object.defineProperty(img, "naturalWidth", { value: ok ? 600 : 0, configurable: true });
  img.decode = ok ? () => Promise.resolve() : () => Promise.reject(new Error("broken"));
};

describe("image-fallback", () => {
  it("tries the fallback, then removes the card image", async () => {
    document.body.innerHTML = `<div class="article-card"><img class="article-image" src="https://a/feed.jpg" data-fallback-src="https://a/page.jpg" alt=""><div class="article-text"></div></div>`;
    const img = document.querySelector<HTMLImageElement>("img")!;
    loaded(img, true);
    await start();

    fail(img);
    expect(img.getAttribute("src")).toBe("https://a/page.jpg");
    expect(img.hasAttribute("data-fallback-src")).toBe(false);
    expect(img.isConnected).toBe(true);

    fail(img);
    expect(document.querySelector("img")).toBeNull();
    expect(document.querySelector(".article-text")).not.toBeNull();
  });

  it("removes a lead image with no fallback", async () => {
    document.body.innerHTML = `<article class="reader"><img class="reader-image" src="https://a/x.jpg" alt=""></article>`;
    const img = document.querySelector<HTMLImageElement>("img")!;
    loaded(img, true);
    await start();
    fail(img);
    expect(document.querySelector("img")).toBeNull();
  });

  it("covers images added after setup", async () => {
    await start();
    document.body.innerHTML = `<img class="article-image" src="https://a/x.jpg" alt="">`;
    fail(document.querySelector("img")!);
    expect(document.querySelector("img")).toBeNull();
  });

  it.each([
    [
      "its link",
      `<p>Before</p><a href="https://a"><img src="https://a/x.jpg"></a><p>After</p>`,
      "<p>Before</p><p>After</p>",
    ],
    ["its paragraph", `<p><img src="https://a/x.jpg"><br></p><p>After</p>`, "<p>After</p>"],
    [
      "its figure and caption",
      `<figure><picture><source srcset="https://a/x.webp"><img src="https://a/x.jpg"></picture><figcaption>A cat</figcaption></figure><p>After</p>`,
      "<p>After</p>",
    ],
    ["only itself beside text", `<p>A cat <img src="https://a/x.jpg"> sat</p>`, "<p>A cat  sat</p>"],
    [
      "only itself beside another image",
      `<figure><img src="https://a/x.jpg"><img src="https://a/y.jpg"></figure>`,
      `<figure><img src="https://a/y.jpg"></figure>`,
    ],
  ])("removes a body image and %s", async (_, html, want) => {
    document.body.innerHTML = `<div class="article-body">${html}</div>`;
    await start();
    fail(document.querySelector('img[src="https://a/x.jpg"]')!);
    expect(document.querySelector(".article-body")!.innerHTML).toBe(want);
  });

  it("leaves other images alone", async () => {
    document.body.innerHTML = `<img class="avatar" src="/icons/x.png" alt="">`;
    await start();
    fail(document.querySelector("img")!);
    expect(document.querySelector("img")).not.toBeNull();
  });

  it("handles an image that failed before setup, not a loaded one", async () => {
    document.body.innerHTML = `
      <img class="article-image" id="broken" src="https://a/broken.jpg" alt="">
      <img class="article-image" id="fine" src="https://a/fine.svg" alt="">`;
    const broken = document.querySelector<HTMLImageElement>("#broken")!;
    const fine = document.querySelector<HTMLImageElement>("#fine")!;
    loaded(broken, false);
    loaded(fine, true);
    Object.defineProperty(fine, "naturalWidth", { value: 0 }); // an SVG with no width
    await start();
    await vi.waitFor(() => expect(broken.isConnected).toBe(false));
    expect(fine.isConnected).toBe(true);
  });
});
