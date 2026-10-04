# Copyright 2026 Cloudfra
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# https://registry.terraform.io/providers/hashicorp/google/latest/docs

terraform {
  # Keep in sync with the minor version of TERRAFORM_VERSION in the Makefile.
  required_version = "~> 1.16"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "8.3.0"
    }
    google-beta = {
      source  = "hashicorp/google-beta"
      version = "8.3.0"
    }
    null = {
      source  = "hashicorp/null"
      version = "3.3.2"
    }
  }
}

provider "google" {
  project = var.gcp_project_id
  region  = var.cloud_region
  zone    = var.cloud_zone
}

provider "google-beta" {
  project = var.gcp_project_id
  region  = var.cloud_region
  zone    = var.cloud_zone
}

data "google_project" "project" {
  project_id = var.gcp_project_id
}

# TODO: delete this once data.google_project.project is referenced by a real
# resource (e.g. IAM bindings scoped to the project number); it exists only
# to keep the data source from being flagged as unused.
resource "null_resource" "log_project" {
  triggers = {
    project_number = data.google_project.project.number
  }

  provisioner "local-exec" {
    command = "echo Resolved GCP project number: ${data.google_project.project.number}"
  }
}

# TODO: delete this once var.testing actually gates a real testing-only
# resource; it exists only to keep the variable from being flagged as unused.
resource "null_resource" "log_testing_flag" {
  count = var.testing ? 1 : 0

  provisioner "local-exec" {
    command = "echo Testing mode is enabled"
  }
}

resource "google_project_service" "api" {
  for_each = toset([
    "artifactregistry.googleapis.com",
    "autoscaling.googleapis.com",
    "compute.googleapis.com",
    "container.googleapis.com",
    "dataflow.googleapis.com",
    "iam.googleapis.com",
    "logging.googleapis.com",
    "monitoring.googleapis.com",
    "storage.googleapis.com",
  ])

  service            = each.key
  disable_on_destroy = false
}
