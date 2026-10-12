# State lives in the Cloudflare R2 bucket rss-tfstate. The endpoint
# (https://<account>.r2.cloudflarestorage.com) comes from AWS_ENDPOINT_URL_S3 or
# -backend-config="endpoints={s3=\"...\"}", and the credentials from
# AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, so no account details are
# committed. See README.md.
terraform {
  backend "s3" {
    bucket       = "rss-tfstate"
    key          = "production.tfstate"
    region       = "auto"
    use_lockfile = true

    # R2 is S3-compatible but isn't AWS.
    use_path_style              = true
    skip_credentials_validation = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
    skip_metadata_api_check     = true
    skip_s3_checksum            = true
  }
}
