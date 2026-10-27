# Nightly pg_dump of the database into the R2 bucket rss-backups, kept 35 days.
# The bucket and its token were created by hand (#208); Terraform only sets its
# lifecycle rules. Restoring: "Backups and restore" in README.md.

resource "cloudflare_r2_bucket_lifecycle" "backups" {
  account_id  = var.cloudflare_account_id
  bucket_name = "rss-backups"

  # These rules replace all of the bucket's rules, so this keeps R2's default
  # rule for incomplete multipart uploads (a backup that failed midway) too.
  # Listed in the order Cloudflare returns them, so the plan compares the right
  # rules (#387).
  rules = [
    {
      id         = "abort-multipart-uploads-after-7-days"
      enabled    = true
      conditions = { prefix = "" }
      abort_multipart_uploads_transition = {
        condition = { type = "Age", max_age = 7 * 24 * 60 * 60 }
      }
    },
    {
      id         = "delete-after-35-days"
      enabled    = true
      conditions = { prefix = "" }
      delete_objects_transition = {
        condition = { type = "Age", max_age = 35 * 24 * 60 * 60 }
      }
    },
  ]

  lifecycle {
    ignore_changes = [
      # The provider reads back an empty condition for the transition a rule
      # doesn't use, which no valid config can match, so every plan would show
      # a change (cloudflare/terraform-provider-cloudflare#7338). Only these
      # unused transitions are ignored; a change to a rule still shows. Remove
      # once the provider is fixed.
      rules[0].delete_objects_transition,
      rules[1].abort_multipart_uploads_transition,
    ]
  }
}

resource "render_cron_job" "backup" {
  name     = "rss-backup"
  plan     = "starter"
  region   = "virginia"
  schedule = "0 8 * * *" # 08:00 UTC

  # Built from deploy/backup by CI. Render pulls the image only when the job is
  # deployed, so deploys (the deploy workflow) pin it by digest with every app
  # deploy; this tag is only the image the job was created with. See README.md.
  runtime_source = {
    image = {
      image_url = "ghcr.io/grodier/rss-backup"
      tag       = "main"
    }
  }

  env_vars = {
    DATABASE_URL = { value = sensitive(render_postgres.rss_db.connection_info.internal_connection_string) }
    # Like the web service, never fall back to plaintext.
    PGSSLMODE             = { value = "require" }
    R2_BUCKET             = { value = "rss-backups" }
    R2_ENDPOINT           = { value = "https://${var.cloudflare_account_id}.r2.cloudflarestorage.com" }
    AWS_ACCESS_KEY_ID     = { value = var.backup_r2_access_key_id }
    AWS_SECRET_ACCESS_KEY = { value = var.backup_r2_secret_access_key }
    AWS_DEFAULT_REGION    = { value = "auto" }
    # Newer aws-cli versions add checksums to uploads by default, which R2 has
    # rejected for streamed uploads. Send them only where S3 requires them.
    AWS_REQUEST_CHECKSUM_CALCULATION = { value = "when_required" }
    AWS_RESPONSE_CHECKSUM_VALIDATION = { value = "when_required" }
  }

  lifecycle {
    ignore_changes = [
      # Deploys (the deploy workflow) set the image by digest; apply must
      # never roll them back.
      runtime_source.image,
    ]
  }
}
