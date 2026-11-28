// The Email Worker that Cloudflare Email Routing's catch-all rule (infra/email.tf)
// runs for every message to the paperround.app zone. It signs the raw message
// and posts it to rss's POST /inbound/email, then turns the answer into the
// SMTP answer while the sender is still connected, so we never send bounces.
// The contract is "Inbound email" under "Security measures" in
// docs/architecture.md; the secret is "Inbound email" in infra/README.md.
//
// Logs carry statuses only, never addresses.
//
// The module exports only its handler: the runtime takes every export for an
// entrypoint. worker.test.ts tests it through email().

interface Env {
  // Plain-text bindings, set by Terraform (infra/email.tf).
  INBOUND_URL: string;
  NEWSLETTER_DOMAIN: string;
  APP_DOMAIN: string;
  // A secret the owner sets, kept out of Terraform; the same value as rss's
  // RSS_INBOUND_SECRET.
  INBOUND_SECRET?: string;
}

// The most the endpoint stores: rss.InboundMaxBytes.
const MAX_BYTES = 5 * 1024 * 1024;

const NO_SUCH_RECIPIENT = "550 5.1.1 No such recipient";
const TOO_BIG = "552 5.3.4 Message too big";
const REFUSED = "550 5.7.1 Message refused";

export default {
  async email(message, env) {
    const to = message.to;
    // Readers' addresses are ASCII, and a header value must be.
    if (!/^[\x21-\x7e]+$/.test(to) || !to.toLowerCase().endsWith("@" + env.NEWSLETTER_DOMAIN)) {
      console.log("inbound: rejected, not the newsletter domain");
      message.setReject(NO_SUCH_RECIPIENT);
      return;
    }
    // The app never mails a reader's address ("Newsletters" in
    // docs/decisions.md), so mail claiming to come from its domains is
    // forged or a loop.
    if (fromDomain(message.from, env.APP_DOMAIN)) {
      console.log("inbound: rejected, from our own domain");
      message.setReject(REFUSED);
      return;
    }
    if (message.rawSize > MAX_BYTES) {
      console.log("inbound: rejected, too big");
      message.setReject(TOO_BIG);
      return;
    }
    if (!env.INBOUND_SECRET) {
      // Throwing is a temporary failure: the sender retries.
      throw new Error("inbound: INBOUND_SECRET isn't set");
    }

    const body = new Uint8Array(await new Response(message.raw).arrayBuffer());
    const t = String(Math.floor(Date.now() / 1000));
    const res = await fetch(env.INBOUND_URL, {
      method: "POST",
      headers: {
        "Content-Type": "message/rfc822",
        "X-Inbound-To": to,
        "X-Inbound-Signature": await signature(env.INBOUND_SECRET, t, to, body),
      },
      body,
      redirect: "manual",
    });
    console.log(`inbound: ${res.status}`);
    switch (res.status) {
      case 202:
        return;
      case 404:
        message.setReject(NO_SUCH_RECIPIENT);
        return;
      case 413:
        message.setReject(TOO_BIG);
        return;
      case 401:
        // INBOUND_SECRET differs from rss's RSS_INBOUND_SECRET.
        console.error("inbound: 401, check INBOUND_SECRET");
        throw new Error("inbound: 401");
      default:
        // 429, 5xx and anything unexpected: a temporary failure.
        throw new Error(`inbound: ${res.status}`);
    }
  },
} satisfies ExportedHandler<Env>;

// fromDomain reports whether the envelope sender's domain is domain or one of
// its subdomains (the newsletter domain is one).
function fromDomain(from: string, domain: string): boolean {
  const at = from.lastIndexOf("@");
  if (at < 0) return false;
  const d = from
    .slice(at + 1)
    .toLowerCase()
    .replace(/\.$/, "");
  return d === domain || d.endsWith("." + domain);
}

// signature returns X-Inbound-Signature's value, "t=<t>,v1=<hex MAC>", whose
// MAC is the HMAC-SHA256, keyed with secret, of t + "\n" + to + "\n" + body:
// inboundMAC in internal/server/inbound.go, which is the spec
// (TestInboundSignatureFixture).
async function signature(secret: string, t: string, to: string, body: Uint8Array): Promise<string> {
  const enc = new TextEncoder();
  const key = await crypto.subtle.importKey("raw", enc.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, [
    "sign",
  ]);
  const head = enc.encode(`${t}\n${to}\n`);
  const data = new Uint8Array(head.length + body.length);
  data.set(head);
  data.set(body, head.length);
  const mac = new Uint8Array(await crypto.subtle.sign("HMAC", key, data));
  return `t=${t},v1=${Array.from(mac, (b) => b.toString(16).padStart(2, "0")).join("")}`;
}
