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

variable "gcp_project_id" {
  description = "Project ID to host the cloud resources."
  type        = string
  # No default: every user of this template must supply their own project ID
  # (-var, a tfvars file, or TF_VAR_gcp_project_id) rather than silently
  # deploying against someone else's project.
}

variable "cloud_region" {
  description = "The default region to place regional resources into."
  type        = string
  default     = "us-west1"
}

variable "cloud_zone" {
  description = "The default zone to place zonal resources into (This should be homed in the cloud_region)."
  type        = string
  default     = "us-west1-b"
}

variable "testing" {
  description = "If true, the testing resources will be created."
  type        = bool
  default     = false
}
