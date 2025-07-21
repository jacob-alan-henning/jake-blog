resource "aws_lightsail_instance" "main" {
  name              = var.instance_name
  availability_zone = "${var.region}${var.availibility_zone}"
  blueprint_id      = var.blueprint_id
  bundle_id         = var.bundle_id
}

resource "aws_lightsail_static_ip" "main" {
  name = "${var.instance_name}-ip"
}

resource "aws_lightsail_static_ip_attachment" "main" {
  static_ip_name = aws_lightsail_static_ip.main.name
  instance_name  = aws_lightsail_instance.main.name
}

resource "aws_lightsail_instance_public_ports" "main" {
  instance_name = aws_lightsail_instance.main.name

  port_info {
    protocol  = "tcp"
    from_port = 80
    to_port   = 80
  }

  port_info {
    protocol  = "tcp"
    from_port = 443
    to_port   = 443
  }
}

resource "aws_lightsail_domain_entry" "blog" {
  domain_name = "jake-henning.com"
  name        = var.domain_name
  type        = "A"
  target      = aws_lightsail_static_ip.main.ip_address
}

resource "aws_s3_bucket" "image-cache" {
  bucket = var.bucket_name
}

resource "aws_s3_bucket_cors_configuration" "image_cache_cors" {
  bucket = aws_s3_bucket.image-cache.id

  cors_rule {
    allowed_headers = ["*"]
    allowed_methods = ["GET"]
    allowed_origins = ["https://${var.domain_name}"]
    expose_headers  = ["ETag"]
  }
}

resource "aws_s3_bucket_public_access_block" "image_cache_pab" {
  bucket = aws_s3_bucket.image-cache.id

  block_public_acls       = true
  block_public_policy     = false
  ignore_public_acls      = true
  restrict_public_buckets = false
}

resource "aws_s3_bucket_policy" "image_cache_policy" {
  bucket = aws_s3_bucket.image-cache.id

  depends_on = [aws_s3_bucket_public_access_block.image_cache_pab]

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "PublicReadGetObject"
        Effect    = "Allow"
        Principal = "*"
        Action    = "s3:GetObject"
        Resource  = "${aws_s3_bucket.image-cache.arn}/*"
      },
      {
        Sid    = "RestrictWriteToCurrentAccount"
        Effect = "Allow"
        Principal = {
          AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"
        }
        Action = [
          "s3:PutObject",
          "s3:PutObjectAcl",
          "s3:DeleteObject"
        ]
        Resource = "${aws_s3_bucket.image-cache.arn}/*"
      }
    ]
  })
}

resource "aws_s3_bucket_server_side_encryption_configuration" "image_cache_encryption" {
  bucket = aws_s3_bucket.image-cache.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_versioning" "image_cache_versioning" {
  bucket = aws_s3_bucket.image-cache.id

  versioning_configuration {
    status = "Enabled"
  }
}
