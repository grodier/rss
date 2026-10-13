variable "signup" {
  description = "Allow new accounts to sign up (RSS_SIGNUP)."
  type        = bool
  default     = false
}

variable "cloudflare_account_id" {
  description = "Cloudflare account ID that holds the R2 buckets (TF_VAR_cloudflare_account_id)."
  type        = string
  # A repository secret like the other IDs, so plans don't print it either.
  sensitive = true
}

variable "backup_r2_access_key_id" {
  description = "Access Key ID of the rss-backups R2 token, which can only read and write the rss-backups bucket."
  type        = string
  sensitive   = true
}

variable "backup_r2_secret_access_key" {
  description = "Secret Access Key of the rss-backups R2 token."
  type        = string
  sensitive   = true
}
