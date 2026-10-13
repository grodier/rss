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
    ]
  }
}
