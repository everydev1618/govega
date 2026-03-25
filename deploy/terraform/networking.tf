resource "aws_security_group" "vega" {
  name        = "vega-${var.customer_name}"
  description = "Vega instance for ${var.customer_name}"

  # HTTP
  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # HTTPS
  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Vega API direct (optional, can remove if only using nginx)
  ingress {
    from_port   = 3001
    to_port     = 3001
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # SSH — left empty, opened/closed dynamically by deploy workflow
  # (the deploy workflow adds/removes a rule for the runner's IP)

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name     = "vega-${var.customer_name}"
    Project  = "vega"
    Customer = var.customer_name
  }
}

resource "aws_eip" "vega" {
  domain = "vpc"

  tags = {
    Name     = "vega-${var.customer_name}"
    Project  = "vega"
    Customer = var.customer_name
  }
}

resource "aws_eip_association" "vega" {
  instance_id   = aws_instance.vega.id
  allocation_id = aws_eip.vega.id
}
