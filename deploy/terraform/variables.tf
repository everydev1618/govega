variable "customer_name" {
  description = "Customer identifier, used for resource naming"
  type        = string
}

variable "fqdn" {
  description = "Fully qualified domain name for this instance (e.g. trevorfountain.synkedup.ai)"
  type        = string
}

variable "instance_type" {
  description = "EC2 instance type"
  type        = string
  default     = "t3.small"
}

variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "manage_dns" {
  description = "Whether to create a Cloudflare DNS record. Set false if DNS is managed externally."
  type        = bool
  default     = true
}

variable "cloudflare_zone_id" {
  description = "Cloudflare zone ID for the domain (required if manage_dns=true)"
  type        = string
  default     = ""
}

variable "dns_record_name" {
  description = "DNS record name within the zone (required if manage_dns=true)"
  type        = string
  default     = ""
}

variable "ssh_public_key" {
  description = "SSH public key for the deploy key pair"
  type        = string
}
