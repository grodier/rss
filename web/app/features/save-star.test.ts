import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { isolate, submit } from "../../shared/test/setup";

isolate();

let fetchMock: ReturnType<typeof vi.fn>;
let formSubmit: ReturnType<typeof vi.spyOn>;

beforeEach(async () => {
  document.body.innerHTML = `
    <form class="save-form" method="post" action="/articles/a1/save" data-toggle>
      <input type="hidden" name="return_to" value="/#article-a1">
      <button class="save-button" type="submit" aria-pressed="false">Save</button>
    </form>`;
  fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
  vi.stubGlobal("fetch", fetchMock);
  formSubmit = vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(() => {});
  (await import("./save-star")).init();
});

afterEach(() => {
  document.documentElement.removeAttribute("style");
});

const form = () => document.querySelector<HTMLFormElement>("form")!;
const button = () => document.querySelector<HTMLButtonElement>("button")!;
const press = async () => {
  expect(submit(form(), button())).toBe(true);
  await vi.waitFor(() => expect(button().hasAttribute("aria-disabled")).toBe(false));
};

describe("save-star", () => {
  it("posts the form with fetch", async () => {
    await press();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe(new URL("/articles/a1/save", location.href).href);
    expect(init.method).toBe("POST");
    expect([...(init.body as URLSearchParams)]).toEqual([["return_to", "/#article-a1"]]);
  });

  it("flips aria-pressed and the action on a 204, and marks a button just turned on", async () => {
    await press();
    expect(button().getAttribute("aria-pressed")).toBe("true");
    expect(form().getAttribute("action")).toBe("/articles/a1/unsave");
    expect("justToggled" in button().dataset).toBe(true);

    await press();
    expect(button().getAttribute("aria-pressed")).toBe("false");
    expect(form().getAttribute("action")).toBe("/articles/a1/save");
    expect("justToggled" in button().dataset).toBe(false);
    expect(formSubmit).not.toHaveBeenCalled();
  });

  it("flips Star's action too", async () => {
    form().setAttribute("action", "/articles/a1/unstar");
    button().setAttribute("aria-pressed", "true");
    await press();
    expect(form().getAttribute("action")).toBe("/articles/a1/star");
  });

  it("ignores presses while the request runs, and shows it's busy only after --delay-busy", async () => {
    vi.useFakeTimers();
    document.documentElement.style.setProperty("--delay-busy", "500ms");
    let respond = (_: Response) => {};
    fetchMock.mockImplementation(() => new Promise<Response>((resolve) => (respond = resolve)));

    submit(form(), button());
    expect(button().getAttribute("aria-disabled")).toBe("true");
    expect(submit(form(), button())).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(499);
    expect("busy" in button().dataset).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect("busy" in button().dataset).toBe(true);

    respond(new Response(null, { status: 204 }));
    await vi.advanceTimersByTimeAsync(0);
    expect("busy" in button().dataset).toBe(false);
    expect(button().hasAttribute("aria-disabled")).toBe(false);
    expect(button().getAttribute("aria-pressed")).toBe("true");
  });

  it.each([
    ["a 200", () => new Response("<p>page</p>", { status: 200 })],
    ["a 500", () => new Response(null, { status: 500 })],
    [
      "a redirect to the login page",
      () => {
        const res = new Response("<p>Log in</p>", { status: 200 });
        Object.defineProperty(res, "redirected", { value: true });
        return res;
      },
    ],
  ])("submits the form normally on %s", async (_, response) => {
    fetchMock.mockImplementation(async () => response());
    submit(form(), button());
    await vi.waitFor(() => expect(formSubmit).toHaveBeenCalledTimes(1));
    expect(button().getAttribute("aria-pressed")).toBe("false");
    expect(button().getAttribute("aria-disabled")).toBe("true"); // stays busy while the page loads
  });

  it("submits the form normally when the request fails", async () => {
    fetchMock.mockImplementation(async () => {
      throw new TypeError("network");
    });
    submit(form(), button());
    await vi.waitFor(() => expect(formSubmit).toHaveBeenCalledTimes(1));
  });

  it("leaves other forms alone", async () => {
    document.body.innerHTML = `<form action="/x" method="post"><button aria-pressed="false">X</button></form>`;
    expect(submit(form(), button())).toBe(false);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
