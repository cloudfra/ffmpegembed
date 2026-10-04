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

terraform {
  # bucket is a placeholder: replace with your own GCS bucket (or configure it
  # per-environment via `terraform init -backend-config="bucket=..."`) before
  # running this template against real infrastructure.
  backend "gcs" {
    bucket = "REPLACE_WITH_YOUR_GCS_BUCKET"
    prefix = "terraform/state"
  }
}