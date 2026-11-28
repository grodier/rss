import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import cases from "./testdata/inbound-signature.json";
import worker from "./worker";

const env = {
  INBOUND_URL: "https://paperround.test/inbound/email",
  NEWSLETTER_DOMAIN: "in.localhost",
  APP_DOMAIN: "paperround.app",
  INBOUND_SECRET: "inbound-test-secret",
};

const raw = "From: news@example.com\r\nSubject: Hello\r\n\r\nHi.\r\n";

interface Sent {
  message: ForwardableEmailMessage;
  rejected: string | undefined;
}

// send delivers a message to the Worker, with fetch answering status, and
// returns what the Worker did with it.
async function send(
  opts: {
    to?: string;
    from?: string;
    body?: Uint8Array;
    rawSize?: number;
    status?: number;
    env?: Partial<typeof env>;
  } = {},
): Promise<Sent> {
  const body = opts.body ?? new TextEncoder().encode(raw);
  let rejected: string | undefined;
  const message = {
    to: opts.to ?? "sam.k7x2m9@in.localhost",
    from: opts.from ?? "news@example.com",
    rawSize: opts.rawSize ?? body.length,
    raw: new Response(body).body,
    headers: new Headers(),
    setReject: (reason: string) => {
      rejected = reason;
    },
  } as unknown as ForwardableEmailMessage;
  fetchMock.mockResolvedValue(new Response(null, { status: opts.status ?? 202 }));
  await worker.email(message, { ...env, ...opts.env });
  return { message, rejected };
}

const fetchMock = vi.fn<typeof fetch>();

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
  vi.spyOn(console, "log").mockImplementation(() => {});
  vi.spyOn(console, "error").mockImplementation(() => {});
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  fetchMock.mockReset();
});

function posted(): { url: string; init: RequestInit; headers: Headers } {
  expect(fetchMock).toHaveBeenCalledOnce();
  const [url, init] = fetchMock.mock.calls[0]!;
  return { url: String(url), init: init!, headers: new Headers(init!.headers) };
}

function base64Bytes(s: string): Uint8Array {
  return Uint8Array.from(atob(s), (c) => c.charCodeAt(0));
}

// The signature is built in Go and rebuilt here: Go is the spec. The fixture
// is written by TestInboundSignatureFixture (internal/server/inbound_test.go).
describe("the signature", () => {
  it("has the fixture's cases", () => {
    expect(cases.length).toBeGreaterThan(3);
  });

  it.each(cases.map((c) => [c.name, c] as const))("%s", async (_, c) => {
    vi.spyOn(Date, "now").mockReturnValue(Number(c.t) * 1000);
    const body = base64Bytes(c.body);
    const { rejected } = await send({ to: c.to, body, env: { INBOUND_SECRET: c.secret } });
    expect(rejected).toBeUndefined();
    const { headers, init } = posted();
    expect(headers.get("X-Inbound-Signature")).toBe(c.signature);
    expect(headers.get("X-Inbound-To")).toBe(c.to);
    expect(new Uint8Array(init.body as Uint8Array)).toEqual(body);
  });
});

describe("posting", () => {
  it("posts the raw message to INBOUND_URL without following redirects", async () => {
    await send();
    const { url, init, headers } = posted();
    expect(url).toBe(env.INBOUND_URL);
    expect(init.method).toBe("POST");
    expect(init.redirect).toBe("manual");
    expect(headers.get("Content-Type")).toBe("message/rfc822");
    expect(new TextDecoder().decode(init.body as Uint8Array)).toBe(raw);
  });

  it("signs with the current time", async () => {
    vi.spyOn(Date, "now").mockReturnValue(1764201600_500);
    await send();
    expect(posted().headers.get("X-Inbound-Signature")).toMatch(/^t=1764201600,v1=[0-9a-f]{64}$/);
  });
});

describe("the answer to the sender", () => {
  it("accepts on 202", async () => {
    const { rejected } = await send({ status: 202 });
    expect(rejected).toBeUndefined();
  });

  it("rejects an unknown address on 404", async () => {
    const { rejected } = await send({ status: 404 });
    expect(rejected).toBe("No such recipient");
  });

  it("rejects a message the app finds too big on 413", async () => {
    const { rejected } = await send({ status: 413 });
    expect(rejected).toBe("Message too big");
  });

  it.each([401, 429, 500, 502, 503, 302])("throws on %i, a temporary failure", async (status) => {
    await expect(send({ status })).rejects.toThrow(`inbound: ${status}`);
  });

  it("says to check the secret on 401", async () => {
    await expect(send({ status: 401 })).rejects.toThrow();
    expect(console.error).toHaveBeenCalledWith("inbound: 401, check INBOUND_SECRET");
  });

  it("throws when the app can't be reached", async () => {
    fetchMock.mockRejectedValue(new TypeError("network error"));
    const message = {
      to: "sam.k7x2m9@in.localhost",
      from: "news@example.com",
      rawSize: raw.length,
      raw: new Response(raw).body,
      setReject: vi.fn(),
    } as unknown as ForwardableEmailMessage;
    await expect(worker.email(message, env)).rejects.toThrow("network error");
    expect(message.setReject).not.toHaveBeenCalled();
  });
});

describe("refused before posting", () => {
  it.each([
    ["the apex", "hello@paperround.app"],
    ["another domain", "sam@example.com"],
    ["a domain ending like it", "sam@notin.localhost"],
    ["non-ASCII", "sàm@in.localhost"],
  ])("a recipient at %s", async (_, to) => {
    const { rejected } = await send({ to });
    expect(rejected).toBe("No such recipient");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("takes the newsletter domain in any case", async () => {
    await send({ to: "Sam.K7X2M9@IN.LOCALHOST" });
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it.each([
    ["our domain", "hello@paperround.app"],
    ["the newsletter domain", "x@in.paperround.app"],
    ["a subdomain, in capitals", "bounce@SEND.PaperRound.App"],
    ["a trailing dot", "hello@paperround.app."],
  ])("a sender at %s", async (_, from) => {
    const { rejected } = await send({ from });
    expect(rejected).toBe("Message refused");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it.each([
    ["a domain ending like ours", "news@notpaperround.app"],
    ["no sender (a bounce)", ""],
  ])("but not a sender with %s", async (_, from) => {
    await send({ from });
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it("a message over 5 MiB", async () => {
    const { rejected } = await send({ rawSize: 5 * 1024 * 1024 + 1 });
    expect(rejected).toBe("Message too big");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("but not one of exactly 5 MiB", async () => {
    await send({ rawSize: 5 * 1024 * 1024 });
    expect(fetchMock).toHaveBeenCalledOnce();
  });
});

describe("without INBOUND_SECRET", () => {
  it("throws, so the sender retries, without posting", async () => {
    await expect(send({ env: { INBOUND_SECRET: "" } })).rejects.toThrow("INBOUND_SECRET");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
