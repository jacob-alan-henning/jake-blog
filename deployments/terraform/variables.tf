variable "region" {
  description = "aws region"
  type        = string
  default     = "us-east-1"
}

variable "availibility_zone" {
  description = "aws availibility zone"
  type        = string
}

variable "domain_name" {
  type = string
}

variable "instance_name" {
  description = "Name of the Lightsail instance"
  type        = string
}

variable "blueprint_id" {
  description = "Blueprint ID for the Lightsail instance"
  type        = string
}

variable "bundle_id" {
  description = "Bundle ID for the Lightsail instance"
  type        = string
}

variable "bucket_name" {
  description = "name of s3 bucket for image cache"
  type        = string
}

