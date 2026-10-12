variable "trusted_proxies" {
  description = "Comma-separated CIDRs of Render's proxies, for RSS_TRUSTED_PROXIES. Empty trusts no proxy."
  type        = string
  default     = ""
}

variable "signup" {
  description = "Allow new accounts to sign up (RSS_SIGNUP)."
  type        = bool
  default     = false
}
