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

  health_check_path  = "/healthcheck"
  pre_deploy_command = "/www migrate"

  # Matches the 30-second graceful shutdown in internal/server/server.go.
  max_shutdown_delay_seconds = 30

  # Its DNS record is cloudflare_dns_record.app in domain.tf.
  custom_domains = [{ name = "rss.georgerodier.com" }]

  env_vars = {
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
    # Email (see "Domain and email" in infra/README.md). The app reads these
    # once #259 adds the flags; until then it ignores them.
    RSS_BASE_URL      = { value = "https://rss.georgerodier.com" }
    RSS_SMTP_HOST     = { value = "smtp.resend.com" }
    RSS_SMTP_PORT     = { value = "465" }
    RSS_SMTP_USERNAME = { value = "resend" }
    RSS_MAIL_FROM     = { value = "rss <hello@rss.georgerodier.com>" }
    RSS_SUPPORT_EMAIL = { value = "rss-support@georgerodier.com" }
  }

  lifecycle {
    ignore_changes = [
      # Deploys (the deploy workflow) set the image by digest; apply must
      # never roll them back.
      runtime_source.image,
      # Set by the "Reset production database" workflow.
      env_vars["RSS_ALLOW_RESET_TO"],
      # Set in Render's dashboard; see "Signing up" in infra/README.md.
      env_vars["RSS_INVITE_CODE"],
      # Set in Render's dashboard, so it never lands in the state; see "Domain
      # and email" in infra/README.md.
      env_vars["RSS_SMTP_PASSWORD"],
    ]
  }
}
