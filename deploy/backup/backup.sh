#!/bin/sh
# Dumps the database at DATABASE_URL and streams it to the S3-compatible
# bucket R2_BUCKET at R2_ENDPOINT, with the AWS_* credentials and region from
# the environment. Restoring a dump: "Backups and restore" in infra/README.md.
set -euo pipefail
name="rss-$(date -u +%Y-%m-%dT%H%MZ).dump"
object="s3://$R2_BUCKET/$name"
# When pg_dump fails, aws still completes the upload with what it got, which
# would leave a truncated dump that looks like a backup. Delete it.
if ! pg_dump --format=custom --no-owner "$DATABASE_URL" |
  aws s3 cp - "$object" --endpoint-url "$R2_ENDPOINT"; then
  aws s3 rm "$object" --endpoint-url "$R2_ENDPOINT" || true
  echo "backup: failed; nothing kept as $name" >&2
  exit 1
fi
echo "backup: uploaded $name"
