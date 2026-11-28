# Inbound email for readers' newsletter addresses (local.newsletter_domain):
# Cloudflare Email Routing receives it, and its catch-all rule runs the Worker
# in email-worker/, which signs each message and posts it to rss's
# POST /inbound/email. See "Inbound email" in README.md, and "Mail arrives
# through Cloudflare Email Routing…" under "Newsletters" in
# docs/decisions.md.

# Enables Email Routing for the newsletter domain and adds its MX and SPF
# records, which Cloudflare locks (route1-3.mx.cloudflare.net, and
# "v=spf1 include:_spf.mx.cloudflare.net ~all"); they aren't
# cloudflare_dns_record resources, which would fight over the locked records.
resource "cloudflare_email_routing_dns" "newsletters" {
  zone_id = var.cloudflare_app_zone_id
  name    = local.newsletter_domain
}

resource "cloudflare_workers_script" "inbound_email" {
  account_id         = var.cloudflare_account_id
  script_name        = "rss-inbound-email"
  main_module        = "worker.js"
  content_file       = "${path.module}/email-worker/dist/worker.js"
  content_sha256     = filesha256("${path.module}/email-worker/dist/worker.js")
  compatibility_date = "2026-11-01"

  bindings = [
    { name = "INBOUND_URL", type = "plain_text", text = "${local.app_url}/inbound/email" },
    { name = "NEWSLETTER_DOMAIN", type = "plain_text", text = local.newsletter_domain },
    # Mail from here, or its subdomains, is refused.
    { name = "APP_DOMAIN", type = "plain_text", text = "paperround.app" },
  ]
  # INBOUND_SECRET is a secret the owner sets with Wrangler or the dashboard,
  # so it never lands in the state; uploads keep it.
  keep_bindings = ["secret_text"]

  # The Worker's logs, for the test sends and a 401 (a mismatched secret).
  observability = {
    enabled = true
    logs    = { enabled = true, invocation_logs = true }
  }
}

# The zone's catch-all, so it also gets mail for paperround.app's other
# addresses, if they ever had MX records: the Worker refuses every address
# outside the newsletter domain.
resource "cloudflare_email_routing_catch_all" "newsletters" {
  zone_id  = var.cloudflare_app_zone_id
  name     = "rss: newsletters to the inbound email Worker"
  enabled  = true
  matchers = [{ type = "all" }]
  actions  = [{ type = "worker", value = [cloudflare_workers_script.inbound_email.script_name] }]

  depends_on = [cloudflare_email_routing_dns.newsletters]
}
