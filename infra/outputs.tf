output "service_id" {
  description = "ID of the Render web service."
  value       = render_web_service.rss.id
}

output "url" {
  description = "The service's onrender.com URL."
  value       = render_web_service.rss.url
}
