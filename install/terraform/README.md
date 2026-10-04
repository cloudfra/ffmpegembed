# Terraform Deployment

```bash
terraform init
terraform plan -var=gcp_project_id=<your-project-id> -var=testing=true
terraform apply -var=gcp_project_id=<your-project-id> -var=testing=true
```
