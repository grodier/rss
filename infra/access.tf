# Cloudflare Access in front of the admin (admin.paperround.app): only the
# owner's email, with a one-time PIN, reaches the admin's own login. It applies
# to proxied DNS records only (domain.tf). See "Deploying the admin" in
# README.md.

resource "cloudflare_zero_trust_access_policy" "admin_owner" {
  account_id = var.cloudflare_account_id
  name       = "rss-admin: owner"
  decision   = "allow"
  include    = [{ email = { email = var.admin_access_email } }]
}

resource "cloudflare_zero_trust_access_application" "admin" {
  account_id       = var.cloudflare_account_id
  name             = "rss-admin"
  type             = "self_hosted"
  domain           = "admin.paperround.app"
  destinations     = [{ type = "public", uri = "admin.paperround.app" }]
  session_duration = "24h"
  policies         = [{ id = cloudflare_zero_trust_access_policy.admin_owner.id, precedence = 1 }]
}

# The deploy workflow checks that /healthcheck reports the deployed commit, as
# it does for rss. It reports only the status, the environment and the
# version, as on paperround.app, so it's open to everyone. Access applies the
# most specific path's application.
resource "cloudflare_zero_trust_access_policy" "admin_healthcheck" {
  account_id = var.cloudflare_account_id
  name       = "rss-admin: healthcheck"
  decision   = "bypass"
  include    = [{ everyone = {} }]
}

resource "cloudflare_zero_trust_access_application" "admin_healthcheck" {
  account_id   = var.cloudflare_account_id
  name         = "rss-admin healthcheck"
  type         = "self_hosted"
  domain       = "admin.paperround.app/healthcheck"
  destinations = [{ type = "public", uri = "admin.paperround.app/healthcheck" }]
  policies     = [{ id = cloudflare_zero_trust_access_policy.admin_healthcheck.id, precedence = 1 }]
}
