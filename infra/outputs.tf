output "service_id" {
  description = "ID of the Render web service."
  value       = render_web_service.rss.id
}

output "admin_service_id" {
  description = "ID of the admin's Render web service (the RENDER_ADMIN_SERVICE_ID secret)."
  value       = render_web_service.admin.id
}

output "backup_job_id" {
  description = "ID of the Render cron job that backs up the database."
  value       = render_cron_job.backup.id
}

output "url" {
  description = "The service's onrender.com URL."
  value       = render_web_service.rss.url
}
