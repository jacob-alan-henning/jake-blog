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
  domain_name = var.domain_name
  name        = var.domain_name
  type        = "A"
  target      = aws_lightsail_static_ip.main.ip_address
}
