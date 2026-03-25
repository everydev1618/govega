output "instance_id" {
  value = aws_instance.vega.id
}

output "elastic_ip" {
  value = aws_eip.vega.public_ip
}

output "security_group_id" {
  value = aws_security_group.vega.id
}

output "fqdn" {
  value = var.fqdn
}
