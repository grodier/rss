import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate } from "../test/setup";
import type { PollSelfOptions } from "./poll-self";

isolate();

let fetchMock: ReturnType<typeof vi.fn>;
let hidden: boolean;

beforeEach(() => {
  vi.useFakeTimers();
  hidden = false;
  vi.spyOn(document, "hidden", "get").mockImplementation(() => hidden);
  fetchMock = vi.fn(async () => new Response("<main></main>", { status: 200 }));
  vi.stubGlobal("fetch", fetchMock);
});

const start = async (opts: Partial<PollSelfOptions> = {}) => {
  const onPage = vi.fn(() => false);
  const { pollSelf } = await import("./poll-self");
  pollSelf({ onPage, ...opts });
  return onPage;
};

const show = () => {
  hidden = false;
  document.dispatchEvent(new Event("visibilitychange"));
};

describe("pollSelf", () => {
  it("fetches the page every 2 s by default, passing each 200 to onPage", async () => {
    const onPage = await start();
    await vi.advanceTimersByTimeAsync(1999);
    expect(fetchMock).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(fetchMock).toHaveBeenCalledWith(location.href, { credentials: "same-origin", cache: "no-store" });
    expect(onPage).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(2000 * 10);
    expect(fetchMock).toHaveBeenCalledTimes(11);
  });

  it("takes its interval and address", async () => {
    await start({ interval: 3000, url: "/timelines/all" });
    await vi.advanceTimersByTimeAsync(2999);
    expect(fetchMock).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(fetchMock).toHaveBeenCalledWith("/timelines/all", { credentials: "same-origin", cache: "no-store" });
  });

  it("stops when onPage says so", async () => {
    const onPage = await start();
    onPage.mockReturnValueOnce(false).mockReturnValueOnce(true);
    await vi.advanceTimersByTimeAsync(2000 * 5);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("follows a redirect and stops", async () => {
    const res = new Response("", { status: 200 });
    Object.defineProperties(res, { redirected: { value: true }, url: { value: "https://x.test/login" } });
    fetchMock.mockResolvedValueOnce(res);
    const onRedirect = vi.fn();
    const onPage = await start({ onRedirect });
    await vi.advanceTimersByTimeAsync(2000 * 3);
    expect(onRedirect).toHaveBeenCalledWith("https://x.test/login");
    expect(onPage).not.toHaveBeenCalled();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("polls again after an error status or a network error", async () => {
    fetchMock.mockResolvedValueOnce(new Response("", { status: 503 })).mockRejectedValueOnce(new Error("offline"));
    const onPage = await start();
    await vi.advanceTimersByTimeAsync(2000 * 3);
    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(onPage).toHaveBeenCalledTimes(1);
  });

  it("stops after maxPolls, whatever the last answer, and calls onGiveUp once", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 503 }));
    const onGiveUp = vi.fn();
    await start({ maxPolls: 3, onGiveUp });
    await vi.advanceTimersByTimeAsync(2000 * 2);
    expect(onGiveUp).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(2000 * 5);
    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(onGiveUp).toHaveBeenCalledTimes(1);
    show();
    await vi.advanceTimersByTimeAsync(2000 * 3);
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("doesn't call onGiveUp when the last poll finished it", async () => {
    const onGiveUp = vi.fn();
    const onPage = await start({ maxPolls: 2, onGiveUp });
    onPage.mockReturnValueOnce(false).mockReturnValueOnce(true);
    await vi.advanceTimersByTimeAsync(2000 * 3);
    expect(onGiveUp).not.toHaveBeenCalled();
  });

  it("waits while hidden and polls at once when shown, without counting the wait", async () => {
    await start({ maxPolls: 2 });
    hidden = true;
    await vi.advanceTimersByTimeAsync(2000 * 5);
    expect(fetchMock).not.toHaveBeenCalled();
    show();
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(2000);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
