# DNS records for rss.georgerodier.com in the georgerodier.com zone. See "Domain
# and email" in README.md. ttl = 1 means automatic.

# DNS-only: Render already puts Cloudflare in front of the service, and a
# proxied record would add a third X-Forwarded-For hop (RSS_PROXY_HOPS).
resource "cloudflare_dns_record" "app" {
  zone_id = var.cloudflare_zone_id
  name    = "rss.georgerodier.com"
  type    = "CNAME"
  content = trimprefix(render_web_service.rss.url, "https://")
  proxied = false
  ttl     = 1
}

# Resend (us-east-1). The names and values are the ones Resend's dashboard
# shows for the domain. send.rss is the return path: Resend serves its bounce
# MX and SPF record behind the CNAME.
resource "cloudflare_dns_record" "resend_return_path" {
  zone_id = var.cloudflare_zone_id
  name    = "send.rss.georgerodier.com"
  type    = "CNAME"
  content = "send.forge.rmta.net"
  proxied = false
  ttl     = 1
}

resource "cloudflare_dns_record" "resend_rsend" {
  zone_id = var.cloudflare_zone_id
  name    = "rsend.rss.georgerodier.com"
  type    = "CNAME"
  content = "rsend.forge.rmta.net"
  proxied = false
  ttl     = 1
}

# A DKIM key is public.
resource "cloudflare_dns_record" "resend_dkim" {
  zone_id = var.cloudflare_zone_id
  name    = "resend._domainkey.rss.georgerodier.com"
  type    = "TXT"
  content = "p=MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDJSmqsZh89wWjOT6+NuFak6JuV0ieZuZke6A0XafEUoJHuu/7KgEkAdGhMFFFsXucAFCyythXcb01MALWShovlkqnY62b2RBlhT6uvoG46pncL+8q8CNIMykxrqJYM1F5F8luJKC7Vjs+vdf7k4C3q3+q50bLUf+PvA+/PKe7VKwIDAQAB"
  ttl     = 1
}

# No _dmarc.rss record: mail from rss.georgerodier.com falls back to the
# p=none record on _dmarc.georgerodier.com, which Cloudflare's DMARC
# Management created and sends reports for. It doesn't support subdomains.
