terraform {
  required_version = ">= 1.5"

  backend "s3" {
    # Configured via -backend-config at init time:
    #   bucket         = "vega-tfstate-214057697325"
    #   key            = "{customer}/terraform.tfstate"
    #   region         = "us-east-1"
    #   dynamodb_table = "vega-terraform-locks"
    #   encrypt        = true
  }

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 4.0"
    }
  }
}

provider "aws" {
  region = var.region
}

provider "cloudflare" {
  # Uses CLOUDFLARE_API_KEY + CLOUDFLARE_EMAIL env vars (Global API Key auth)
}

data "aws_ami" "ubuntu" {
  most_recent = true
  owners      = ["099720109477"] # Canonical

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}
