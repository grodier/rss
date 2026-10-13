# The nightly database backup (render_cron_job.backup in infra/backup.tf). The
# Postgres major version matches the database's: pg_dump can't dump a newer
# server. Configuration arrives at runtime; see backup.sh.
FROM postgres:17-alpine

RUN apk add --no-cache aws-cli

COPY backup.sh /backup.sh

ENTRYPOINT ["/backup.sh"]
