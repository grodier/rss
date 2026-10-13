terraform {
  required_version = ">= 1.10"

  required_providers {
    render = {
      source  = "render-oss/render"
      version = "~> 1.9"
    }
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 5.27"
    }
  }
}

# The provider reads RENDER_API_KEY and RENDER_OWNER_ID from the environment.
provider "render" {}

# The provider reads CLOUDFLARE_API_TOKEN (the rss-terraform token) from the
# environment.
provider "cloudflare" {}
