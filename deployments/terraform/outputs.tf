output "instance_ip" {
  description = "Public IP of the Lightsail instance"
  value       = aws_lightsail_static_ip.main.ip_address
}

output "instance_arn" {
  description = "ARN of the Lightsail instance"
  value       = aws_lightsail_instance.main.arn
}

output "dns_name" {
  description = "Full DNS name for the blog"
  value       = "blog.${var.domain_name}"
}