resource "aws_key_pair" "deploy" {
  key_name   = "vega-${var.customer_name}-deploy"
  public_key = var.ssh_public_key
}

resource "aws_instance" "vega" {
  ami                    = data.aws_ami.ubuntu.id
  instance_type          = var.instance_type
  key_name               = aws_key_pair.deploy.key_name
  vpc_security_group_ids = [aws_security_group.vega.id]

  iam_instance_profile = aws_iam_instance_profile.vega.name

  user_data = templatefile("${path.module}/user-data.sh.tpl", {
    customer_name = var.customer_name
    fqdn          = var.fqdn
    region        = var.region
  })

  root_block_device {
    volume_size = 20
    volume_type = "gp3"
  }

  tags = {
    Name    = "vega-${var.customer_name}"
    Project = "vega"
    Customer = var.customer_name
  }
}

# IAM role so the instance can read its own SSM parameters
resource "aws_iam_role" "vega" {
  name = "vega-${var.customer_name}-ec2"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "ec2.amazonaws.com"
      }
    }]
  })
}

resource "aws_iam_role_policy" "ssm_read" {
  name = "ssm-read"
  role = aws_iam_role.vega.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "ssm:GetParameter",
        "ssm:GetParameters"
      ]
      Resource = "arn:aws:ssm:${var.region}:*:parameter/vega/${var.customer_name}/*"
    }]
  })
}

resource "aws_iam_instance_profile" "vega" {
  name = "vega-${var.customer_name}"
  role = aws_iam_role.vega.name
}
