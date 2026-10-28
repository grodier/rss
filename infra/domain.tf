# DNS records for the app: paperround.app in its own zone, and the old host
# rss.georgerodier.com in the georgerodier.com zone. See "Domain and email" in
# README.md. ttl = 1 means automatic.
#
# All DNS-only: Render already puts Cloudflare in front of the service, and a
# proxied record would add a third X-Forwarded-For hop (RSS_PROXY_HOPS).

# A CNAME at the apex, which Cloudflare flattens.
resource "cloudflare_dns_record" "app_apex" {
  zone_id = var.cloudflare_app_zone_id
  name    = "paperround.app"
  type    = "CNAME"
  content = trimprefix(render_web_service.rss.url, "https://")
  proxied = false
  ttl     = 1
}

# Render redirects www to the apex once the record points at it.
resource "cloudflare_dns_record" "app_www" {
  zone_id = var.cloudflare_app_zone_id
  name    = "www.paperround.app"
  type    = "CNAME"
  content = trimprefix(render_web_service.rss.url, "https://")
  proxied = false
  ttl     = 1
}

# The old host, kept so old links and bookmarks keep working.
resource "cloudflare_dns_record" "app" {
  zone_id = var.cloudflare_zone_id
  name    = "rss.georgerodier.com"
  type    = "CNAME"
  content = trimprefix(render_web_service.rss.url, "https://")
  proxied = false
  ttl     = 1
}

# Resend (us-east-1). The names and values are the ones Resend's dashboard
# shows for paperround.app. send is the return path: Resend serves its bounce
# MX and SPF record behind the CNAME.
resource "cloudflare_dns_record" "resend_return_path" {
  zone_id = var.cloudflare_app_zone_id
  name    = "send.paperround.app"
  type    = "CNAME"
  content = "send.forge.rmta.net"
  proxied = false
  ttl     = 1
}

resource "cloudflare_dns_record" "resend_rsend" {
  zone_id = var.cloudflare_app_zone_id
  name    = "rsend.paperround.app"
  type    = "CNAME"
  content = "rsend.forge.rmta.net"
  proxied = false
  ttl     = 1
}

# A DKIM key is public.
resource "cloudflare_dns_record" "resend_dkim" {
  zone_id = var.cloudflare_app_zone_id
  name    = "resend._domainkey.paperround.app"
  type    = "TXT"
  content = "p=MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQCyOzin+uoa7XlEC/fT457AbJ8nHOunM9Vg71jGFq51dwWm5hBPJKyjel5wxcoxp4srp6a4pl+onUEmhcfzmStty05lVwYU3ka6sDyxOBROEqUXm3tezRC55zX6QGoG+cCuOjxVYjVssAbUeAYwJm3obSJ3IBrRe3je3KuwxxLFCwIDAQAB"
  ttl     = 1
}

# No _dmarc record: Cloudflare's DMARC Management created the p=none record
# on _dmarc.paperround.app, and sends reports for it.
