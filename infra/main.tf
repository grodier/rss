resource "render_postgres" "rss_db" {
  name          = "rss-db"
  plan          = "basic_256mb"
  region        = "virginia"
  version       = "17"
  database_name = "rss"

  # No external access: only services on Render's private network connect.
  ip_allow_list = []

  lifecycle {
    prevent_destroy = true
  }
}

# Email settings both services use (see "Domain and email" in README.md); the
# SMTP password is set in Render's dashboard, on each service.
locals {
  mail_env_vars = {
    RSS_SMTP_HOST     = { value = "smtp.resend.com" }
    RSS_SMTP_PORT     = { value = "465" }
    RSS_SMTP_USERNAME = { value = "resend" }
    RSS_MAIL_FROM     = { value = "rss <hello@paperround.app>" }
    RSS_SUPPORT_EMAIL = { value = "rss-support@georgerodier.com" }
  }
}

resource "render_web_service" "rss" {
  name   = "rss"
  plan   = "starter"
  region = "virginia"

  runtime_source = {
    image = {
      image_url = "ghcr.io/grodier/rss"
      tag       = "main"
    }
  }

  health_check_path = "/healthcheck"

  # One command (the image has no shell): migrate, then queue lookups of the
  # seed list's sites not found yet (internal/seed). Local resets don't seed.
  pre_deploy_command = "/www migrate -seed"

  # Matches the 30-second graceful shutdown in internal/server/server.go.
  max_shutdown_delay_seconds = 30

  # Their DNS records are in domain.tf. Render adds www.paperround.app with
  # the apex and redirects it there; rss.georgerodier.com is the old host.
  custom_domains = [{ name = "paperround.app" }, { name = "rss.georgerodier.com" }]

  env_vars = merge(local.mail_env_vars, {
    RSS_ENV = { value = "production" }
    # Internal connections support TLS, so lib/pq's default sslmode=require
    # works and never falls back to plaintext.
    RSS_DB_DSN            = { value = sensitive(render_postgres.rss_db.connection_info.internal_connection_string) }
    RSS_DB_MAX_OPEN_CONNS = { value = "10" }
    # Requests come through Cloudflare, then Render's proxy, and each appends
    # one X-Forwarded-For entry. Checked in #214; see infra/README.md.
    RSS_PROXY_HOPS = { value = "2" }
    # Render routes to PORT; the app listens on RSS_PORT.
    RSS_PORT = { value = "8080" }
    PORT     = { value = "8080" }
    # Email (see "Domain and email" in infra/README.md); the rest of the mail
    # settings are in local.mail_env_vars.
    RSS_BASE_URL = { value = "https://paperround.app" }
  })

  lifecycle {
    ignore_changes = [
      # Deploys (the deploy workflow) set the image by digest; apply must
      # never roll them back.
      runtime_source.image,
      # Set in Render's dashboard: the accounts allowed debug mode.
      env_vars["RSS_DEBUG_EMAILS"],
      # Set in Render's dashboard, so it never lands in the state; see "Domain
      # and email" in infra/README.md.
      env_vars["RSS_SMTP_PASSWORD"],
    ]
  }
}

# The admin site (cmd/admin), its own service from its own image, built from the
# same commit as rss's (the Dockerfile's admin stage). Cloudflare Access is in
# front of admin.paperround.app (access.tf), and the service's onrender.com URL
# is disabled in Render's dashboard, so Access can't be bypassed; the admin's
# login is the real gate. See "Admin" in docs/decisions.md and "Deploying the
# admin" in README.md.
resource "render_web_service" "admin" {
  name   = "rss-admin"
  plan   = "starter"
  region = "virginia"

  # The image's entrypoint is /admin, which serves.
  runtime_source = {
    image = {
      image_url = "ghcr.io/grodier/rss-admin"
      tag       = "main"
    }
  }

  # Render checks it on the service's internal address, not through Access.
  health_check_path = "/healthcheck"

  # Matches the 30-second graceful shutdown in internal/admin/server.go.
  max_shutdown_delay_seconds = 30

  # Its DNS record is in domain.tf.
  custom_domains = [{ name = "admin.paperround.app" }]

  # The admin's commands (admin invite, admin send-test-email) read the mail
  # settings and RSS_APP_URL too, run from the service's Shell.
  env_vars = merge(local.mail_env_vars, {
    RSS_ENV    = { value = "production" }
    RSS_DB_DSN = { value = sensitive(render_postgres.rss_db.connection_info.internal_connection_string) }
    # It shares the database's connections with rss: see "A second service on
    # the database's connections" in docs/backlog.md.
    RSS_DB_MAX_OPEN_CONNS = { value = "3" }
    # Requests come through the paperround.app zone's Cloudflare (its record
    # is proxied, for Access), Render's Cloudflare, then Render's proxy, and
    # each appends one X-Forwarded-For entry; see "Checking the client IP" in
    # README.md.
    RSS_PROXY_HOPS = { value = "3" }
    RSS_PORT       = { value = "8080" }
    PORT           = { value = "8080" }
    RSS_BASE_URL   = { value = "https://admin.paperround.app" }
    # The reader's URL, for the links admin invite create prints.
    RSS_APP_URL = { value = "https://paperround.app" }
    # rss's address on the private network, whose /healthcheck the admin's
    # pinger checks every minute.
    RSS_APP_INTERNAL_URL = { value = var.app_internal_url }
    # rss's ID, whose CPU, memory and deploys the service page reads from
    # Render's API with RSS_RENDER_API_KEY.
    RSS_RENDER_SERVICE_ID = { value = render_web_service.rss.id }
  })

  lifecycle {
    ignore_changes = [
      # Deploys (the deploy workflow) set the image by digest; apply must
      # never roll them back.
      runtime_source.image,
      # Set in Render's dashboard, so none lands in the state: the key the
      # admins' TOTP secrets are sealed with, the SMTP password, and the
      # Render API key the service page reads rss's figures with.
      env_vars["RSS_SECRET_KEY"],
      env_vars["RSS_SMTP_PASSWORD"],
      env_vars["RSS_RENDER_API_KEY"],
    ]
  }
}
