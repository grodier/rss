import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate, liveRegion, pageTransition, submit } from "../../shared/test/setup";

isolate();

let fetchMock: ReturnType<typeof vi.fn>;

// redirect is what fetch resolves to on a redirect it doesn't follow
// (redirect: "manual"), which Response's constructor can't make.
const redirect = () => ({ type: "opaqueredirect", status: 0, ok: false, redirected: false }) as Response;
let formSubmit: ReturnType<typeof vi.spyOn>;

beforeEach(async () => {
  document.body.innerHTML = `
    <form class="save-form" method="post" action="/articles/a1/save" data-toggle>
      <input type="hidden" name="return_to" value="/#article-a1">
      <button class="save-button" type="submit" aria-pressed="false" aria-label="Save" title="Save"></button>
    </form>`;
  fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
  vi.stubGlobal("fetch", fetchMock);
  formSubmit = vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(() => {});
  (await import("./save-star")).init();
});

const form = () => document.querySelector<HTMLFormElement>("form")!;
const button = () => document.querySelector<HTMLButtonElement>("button")!;
const press = async () => {
  expect(submit(form(), button())).toBe(true);
  await vi.waitFor(() => expect(button().hasAttribute("aria-disabled")).toBe(false));
};

// pending makes fetch return promises the test resolves, one per call, in
// order.
function pending(): ((res: Response | Error) => Promise<void>)[] {
  const responds: ((res: Response | Error) => Promise<void>)[] = [];
  fetchMock.mockImplementation(
    () =>
      new Promise<Response>((resolve, reject) => {
        responds.push(async (res) => {
          if (res instanceof Error) reject(res);
          else resolve(res);
          await new Promise((r) => setTimeout(r, 0)); // let the script go on
        });
      }),
  );
  return responds;
}

const noContent = () => new Response(null, { status: 204 });
const path = (call: number) => new URL(fetchMock.mock.calls[call][0] as string).pathname;

describe("save-star", () => {
  it("posts the form with fetch", async () => {
    expect(submit(form(), button())).toBe(true);
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe(new URL("/articles/a1/save", location.href).href);
    expect(init.method).toBe("POST");
    expect(init.redirect).toBe("manual");
    expect(init.keepalive).toBe(true);
    expect([...(init.body as URLSearchParams)]).toEqual([["return_to", "/#article-a1"]]);
  });

  it.each([
    ["Save", "/articles/a1/save", "/articles/a1/unsave"],
    ["Star", "/articles/a1/star", "/articles/a1/unstar"],
  ])(
    "flips %s's aria-pressed and action before the server answers, and marks it just turned on",
    async (_, on, off) => {
      form().setAttribute("action", on);
      form().dataset.announcePressed = "Saved";
      form().dataset.announceUnpressed = "Unsaved";
      const responds = pending();

      expect(submit(form(), button())).toBe(true);
      expect(button().getAttribute("aria-pressed")).toBe("true");
      expect(form().getAttribute("action")).toBe(off);
      expect("justToggled" in button().dataset).toBe(true);
      expect(button().hasAttribute("aria-disabled")).toBe(false);
      expect("busy" in button().dataset).toBe(false);
      await vi.waitFor(() => expect(liveRegion()).toBe("Saved"));
      expect(path(0)).toBe(on);

      await responds[0](noContent());
      expect(button().getAttribute("aria-pressed")).toBe("true");
      expect(form().getAttribute("action")).toBe(off);
      expect(formSubmit).not.toHaveBeenCalled();

      submit(form(), button());
      expect(button().getAttribute("aria-pressed")).toBe("false");
      expect(form().getAttribute("action")).toBe(on);
      expect("justToggled" in button().dataset).toBe(false);
      await vi.waitFor(() => expect(liveRegion()).toBe("Unsaved"));
      expect(path(1)).toBe(off);
      await responds[1](noContent());
      expect(button().getAttribute("aria-pressed")).toBe("false");
      expect(fetchMock).toHaveBeenCalledTimes(2);
      expect(formSubmit).not.toHaveBeenCalled();
    },
  );

  describe("on a failure", () => {
    // submitted records the action the form had when it was submitted.
    let submitted: string[];
    beforeEach(() => {
      submitted = [];
      formSubmit.mockImplementation(function (this: HTMLFormElement) {
        submitted.push(this.getAttribute("action")!);
      });
    });

    it.each([
      ["a redirect, such as to the login page", () => redirect()],
      ["a 500", () => new Response(null, { status: 500 })],
      ["a 200", () => new Response("<p>page</p>", { status: 200 })],
      ["a failed request", () => new TypeError("network")],
    ])("puts the button back and submits the form on %s", async (_, response) => {
      const responds = pending();
      submit(form(), button());
      expect(button().getAttribute("aria-pressed")).toBe("true");

      await responds[0](response());
      expect(submitted).toEqual(["/articles/a1/save"]);
      expect(button().getAttribute("aria-pressed")).toBe("false");
      expect(form().getAttribute("action")).toBe("/articles/a1/save");
      expect("justToggled" in button().dataset).toBe(false);
      // Presses wait for the page to load.
      expect(button().getAttribute("aria-disabled")).toBe("true");
      expect(submit(form(), button())).toBe(true);
      expect(fetchMock).toHaveBeenCalledTimes(1);
      expect(button().getAttribute("aria-pressed")).toBe("false");
    });

    it("goes back to the last confirmed state, not the one before the last press", async () => {
      const responds = pending();
      submit(form(), button()); // on
      await responds[0](noContent());
      submit(form(), button()); // off
      await responds[1](redirect());
      expect(submitted).toEqual(["/articles/a1/unsave"]);
      expect(button().getAttribute("aria-pressed")).toBe("true");
      expect(form().getAttribute("action")).toBe("/articles/a1/unsave");
      expect("justToggled" in button().dataset).toBe(false);
    });

    it("submits the form for the state wanted, even when it's the confirmed one", async () => {
      const responds = pending();
      submit(form(), button()); // on
      submit(form(), button()); // off again, while the first request runs
      await responds[0](redirect());
      expect(submitted).toEqual(["/articles/a1/unsave"]);
      expect(button().getAttribute("aria-pressed")).toBe("false");
      expect(form().getAttribute("action")).toBe("/articles/a1/save");
    });
  });

  it("sends a button's requests one at a time, the last press winning", async () => {
    const responds = pending();
    submit(form(), button()); // on
    submit(form(), button()); // off
    expect(button().getAttribute("aria-pressed")).toBe("false");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(path(0)).toBe("/articles/a1/save");

    await responds[0](noContent());
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(path(1)).toBe("/articles/a1/unsave");
    submit(form(), button()); // on, while the second runs
    submit(form(), button()); // off
    expect(fetchMock).toHaveBeenCalledTimes(2);

    await responds[1](noContent());
    expect(fetchMock).toHaveBeenCalledTimes(2); // off is confirmed
    expect(button().getAttribute("aria-pressed")).toBe("false");
    expect(form().getAttribute("action")).toBe("/articles/a1/save");
    expect(formSubmit).not.toHaveBeenCalled();
  });

  it("sends a press waiting behind a running request at once when the page is hidden", async () => {
    pending();
    submit(form(), button()); // on
    submit(form(), button()); // off, waiting for the first request
    expect(fetchMock).toHaveBeenCalledTimes(1);

    pageTransition("pagehide", false);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(path(1)).toBe("/articles/a1/unsave");
    expect(fetchMock.mock.calls[1][1].keepalive).toBe(true);
  });

  it("sends nothing more when the page is hidden while the running request is the one wanted", async () => {
    const responds = pending();
    submit(form(), button()); // on
    pageTransition("pagehide", false);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    // Nor once it has finished.
    await responds[0](noContent());
    submit(form(), button()); // off
    await responds[1](noContent());
    pageTransition("pagehide", true);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("sends one request for three quick presses ending on", async () => {
    const responds = pending();
    submit(form(), button());
    submit(form(), button());
    submit(form(), button());
    expect(button().getAttribute("aria-pressed")).toBe("true");
    expect("justToggled" in button().dataset).toBe(true);

    await responds[0](noContent());
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(button().getAttribute("aria-pressed")).toBe("true");
    expect(form().getAttribute("action")).toBe("/articles/a1/unsave");
  });

  it("gives the button back when the page comes back from the back/forward cache", async () => {
    fetchMock.mockImplementation(async () => redirect());
    submit(form(), button());
    await vi.waitFor(() => expect(formSubmit).toHaveBeenCalledTimes(1));
    expect(button().getAttribute("aria-disabled")).toBe("true");

    // A page left without being restored stays waiting.
    pageTransition("pageshow", false);
    expect(button().getAttribute("aria-disabled")).toBe("true");

    pageTransition("pageshow", true);
    expect(button().hasAttribute("aria-disabled")).toBe(false);
    expect(button().getAttribute("aria-pressed")).toBe("false");

    fetchMock.mockImplementation(async () => noContent());
    submit(form(), button());
    expect(button().getAttribute("aria-pressed")).toBe("true");
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(path(1)).toBe("/articles/a1/save");
    expect(formSubmit).toHaveBeenCalledTimes(1);
  });

  describe("Follow on a page that manages the feed", () => {
    beforeEach(() => {
      document.body.innerHTML = `
        <form class="follow-form" method="post" action="/feeds/f1/subscribe" data-toggle
            data-announce-pressed="Following Example" data-announce-unpressed="Unfollowed Example">
          <input type="hidden" name="return_to" value="/discover#feed-f1">
          <button class="follow-button" type="submit" aria-pressed="false" aria-label="Follow Example">Follow</button>
        </form>`;
    });

    it("follows, then unfollows, announcing each", async () => {
      await press();
      expect(fetchMock.mock.calls[0][0]).toBe(new URL("/feeds/f1/subscribe", location.href).href);
      expect(button().getAttribute("aria-pressed")).toBe("true");
      expect(form().getAttribute("action")).toBe("/feeds/f1/unsubscribe");
      await vi.waitFor(() => expect(liveRegion()).toBe("Following Example"));

      await press();
      expect(fetchMock.mock.calls[1][0]).toBe(new URL("/feeds/f1/unsubscribe", location.href).href);
      expect(button().getAttribute("aria-pressed")).toBe("false");
      expect(form().getAttribute("action")).toBe("/feeds/f1/subscribe");
      await vi.waitFor(() => expect(liveRegion()).toBe("Unfollowed Example"));
      expect(formSubmit).not.toHaveBeenCalled();
    });

    it("waits for the server, ignoring presses while the request runs", async () => {
      const responds = pending();
      submit(form(), button());
      expect(button().getAttribute("aria-disabled")).toBe("true");
      expect(button().getAttribute("aria-pressed")).toBe("false");
      expect(submit(form(), button())).toBe(true);
      expect(fetchMock).toHaveBeenCalledTimes(1);

      await responds[0](noContent());
      expect(button().hasAttribute("aria-disabled")).toBe(false);
      expect(button().getAttribute("aria-pressed")).toBe("true");
    });

    it("goes to the confirmation, rather than toggling, when unfollowing redirects there", async () => {
      form().setAttribute("action", "/feeds/f1/unsubscribe");
      button().setAttribute("aria-pressed", "true");
      fetchMock.mockImplementation(async () => redirect());
      submit(form(), button());
      await vi.waitFor(() => expect(formSubmit).toHaveBeenCalledTimes(1));
      expect(fetchMock.mock.calls[0][0]).toBe(new URL("/feeds/f1/unsubscribe", location.href).href);
      expect(button().getAttribute("aria-pressed")).toBe("true");
      expect(form().getAttribute("action")).toBe("/feeds/f1/unsubscribe");
      expect(liveRegion()).not.toBe("Unfollowed Example");
    });

    it("submits the form normally on anything but a 204, such as a gone feed's redirect", async () => {
      fetchMock.mockImplementation(async () => redirect());
      submit(form(), button());
      await vi.waitFor(() => expect(formSubmit).toHaveBeenCalledTimes(1));
      expect(button().getAttribute("aria-pressed")).toBe("false");
    });
  });

  describe("Follow in a list for finding feeds", () => {
    beforeEach(() => {
      document.body.innerHTML = `
        <li id="feed-f1">
          <form class="follow-form" method="post" action="/feeds/f1/subscribe" data-toggle data-follow-once
              data-announce-pressed="Following Example" data-announce-unpressed="Unfollowed Example">
            <input type="hidden" name="return_to" value="/discover#feed-f1">
            <button class="follow-button" type="submit" aria-pressed="false" aria-label="Follow Example">Follow</button>
            <a class="following-label" href="/feeds/f1" aria-label="Following Example: go to the feed" hidden>✓ Following</a>
          </form>
        </li>`;
    });
    const label = () => document.querySelector<HTMLAnchorElement>(".following-label")!;

    it("puts the focused, shown ✓ Following in the form's place, and announces it", async () => {
      expect(submit(form(), button())).toBe(true);
      await vi.waitFor(() => expect(document.querySelector("form")).toBeNull());
      expect(fetchMock.mock.calls[0][0]).toBe(new URL("/feeds/f1/subscribe", location.href).href);
      expect(label().hidden).toBe(false);
      expect(label().parentElement!.id).toBe("feed-f1");
      expect(document.activeElement).toBe(label());
      await vi.waitFor(() => expect(liveRegion()).toBe("Following Example"));
      expect(formSubmit).not.toHaveBeenCalled();
    });

    it("keeps Follow and submits the form normally on anything but a 204", async () => {
      fetchMock.mockImplementation(async () => redirect());
      submit(form(), button());
      await vi.waitFor(() => expect(formSubmit).toHaveBeenCalledTimes(1));
      expect(button().getAttribute("aria-pressed")).toBe("false");
      expect(label().hidden).toBe(true);
    });
  });

  it("leaves other forms alone", async () => {
    document.body.innerHTML = `<form action="/x" method="post"><button aria-pressed="false">X</button></form>`;
    expect(submit(form(), button())).toBe(false);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
