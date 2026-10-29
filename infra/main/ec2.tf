data "aws_vpc" "default" {
  default = true
}

data "aws_subnets" "default" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.default.id]
  }
}

data "aws_ssm_parameter" "al2023_arm64" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-arm64"
}

resource "aws_security_group" "vpn" {
  name        = "gamevpn"
  description = "WireGuard"
  vpc_id      = data.aws_vpc.default.id
}

resource "aws_vpc_security_group_ingress_rule" "wg" {
  security_group_id = aws_security_group.vpn.id
  ip_protocol       = "udp"
  from_port         = var.wg_port
  to_port           = var.wg_port
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "all" {
  security_group_id = aws_security_group.vpn.id
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_iam_role" "vpn" {
  name = "gamevpn-node"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "ssm_core" {
  count = var.node_session_manager ? 1 : 0

  role       = aws_iam_role.vpn.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_role_policy" "put_pubkey" {
  name = "put-server-public-key"
  role = aws_iam_role.vpn.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "ssm:PutParameter"
      Resource = aws_ssm_parameter.server_public_key.arn
    }]
  })
}

# The node sees only the work queue and its own record; sessions, leases and health stay out of reach.
resource "aws_iam_role_policy" "node_table" {
  name = "work-queue"
  role = aws_iam_role.vpn.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["dynamodb:Query", "dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem"]
      Resource = aws_dynamodb_table.gamevpn.arn
      Condition = {
        "ForAllValues:StringEquals" = { "dynamodb:LeadingKeys" = ["WORK", "NODE"] }
      }
    }]
  })
}

resource "aws_iam_role_policy" "node_artifacts" {
  name = "artifacts"
  role = aws_iam_role.vpn.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = "s3:GetObject"
        Resource = ["${aws_s3_bucket.artifacts.arn}/node/*", "${aws_s3_bucket.artifacts.arn}/agent/*"]
      },
      {
        Effect    = "Allow"
        Action    = "s3:ListBucket"
        Resource  = aws_s3_bucket.artifacts.arn
        Condition = { StringLike = { "s3:prefix" = ["node/*", "node/", "agent/*"] } }
      },
    ]
  })
}

resource "aws_iam_instance_profile" "vpn" {
  name = "gamevpn-node"
  role = aws_iam_role.vpn.name
}

resource "aws_instance" "vpn" {
  ami                                  = data.aws_ssm_parameter.al2023_arm64.value
  instance_type                        = "t4g.nano"
  subnet_id                            = sort(data.aws_subnets.default.ids)[0]
  vpc_security_group_ids               = [aws_security_group.vpn.id]
  iam_instance_profile                 = aws_iam_instance_profile.vpn.name
  associate_public_ip_address          = true
  instance_initiated_shutdown_behavior = "stop"

  metadata_options {
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
  }

  root_block_device {
    volume_type = "gp3"
    volume_size = 8
    encrypted   = true
  }

  user_data = templatefile("${path.module}/../../node/cloud-init.yaml", {
    bucket         = aws_s3_bucket.artifacts.id
    region         = "us-west-2"
    table          = aws_dynamodb_table.gamevpn.name
    pubkey_param   = aws_ssm_parameter.server_public_key.name
    wg_address     = var.wg_address
    wg_port        = var.wg_port
    wg_subnet      = var.wg_subnet
    boot_grace     = "${var.boot_grace_seconds}s"
    idle_stop      = "${var.idle_stop_seconds}s"
    handshake_idle = "${var.handshake_idle_seconds}s"
  })

  # A replacement regenerates the server key and kills live sessions, so node changes ship through S3 instead.
  # A stopped instance reports no public IP, which the provider would otherwise read as drift and replace the node.
  lifecycle {
    ignore_changes = [ami, user_data, associate_public_ip_address]
  }

  depends_on = [aws_s3_object.node, aws_s3_object.agent]

  tags = { Name = "gamevpn" }
}
