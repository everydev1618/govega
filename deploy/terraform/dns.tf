resource "cloudflare_record" "vega" {
  count   = var.manage_dns ? 1 : 0
  zone_id = var.cloudflare_zone_id
  name    = var.dns_record_name
  content = aws_eip.vega.public_ip
  type    = "A"
  ttl     = 300
  proxied = false
}
