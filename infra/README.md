# infra

Terraform for production on Render: one Postgres 17 database (`rss-db`), one web service (`rss`) running `ghcr.io/grodier/rss`, and one cron job (`rss-backup`) that dumps the database to the Cloudflare R2 bucket `rss-backups` every night, all in Virginia.
State lives in the R2 bucket `rss-tfstate`.
Why Render and this setup: "Deployment" in [`docs/decisions.md`](../docs/decisions.md).

Terraform doesn't own four things that change outside it, and ignores them:

- **The images** of the web service and the backup cron job. Deploys set them by digest; `apply` never rolls a deploy back.
- **`RSS_ALLOW_RESET_TO`**, set by the "Reset production database" workflow.
- **`RSS_INVITE_CODE`**, set in Render's dashboard (see "Signing up").
- **`RSS_SMTP_PASSWORD`**, set in Render's dashboard (see "Domain and email").

## Prerequisites

The accounts, buckets, tokens and GitHub settings from #208, and Terraform 1.10 or later.

## Running Terraform locally

Since the first apply (#214), applies run only from GitHub Actions (see "Approving infrastructure applies").
Locally, run `plan` and `output`, never `apply`.
Take every value from your password manager, never from a file in the repository:

```sh
export AWS_ACCESS_KEY_ID=...        # the rss-tfstate R2 token's Access Key ID
export AWS_SECRET_ACCESS_KEY=...    # the rss-tfstate R2 token's Secret Access Key
export AWS_ENDPOINT_URL_S3=https://<account ID>.r2.cloudflarestorage.com
export RENDER_API_KEY=...
export RENDER_OWNER_ID=...          # starts with tea- or usr-
export CLOUDFLARE_API_TOKEN=...     # the rss-terraform token
export TF_VAR_cloudflare_account_id=...
export TF_VAR_cloudflare_zone_id=...           # the georgerodier.com zone's ID
export TF_VAR_backup_r2_access_key_id=...      # the rss-backups R2 token's Access Key ID
export TF_VAR_backup_r2_secret_access_key=...  # the rss-backups R2 token's Secret Access Key
```

Then, from the repository root:

```sh
terraform -chdir=infra init
terraform -chdir=infra plan
```

In a plan, nothing should be destroyed or replaced unless you meant it, and no secret should appear in plain text.
The database has `prevent_destroy`.

`terraform -chdir=infra output -raw url` prints the service's URL, and `output -raw service_id` its ID.

### With 1Password

`op run` hands the values to one Terraform command without exporting them into your shell, and masks them if Terraform prints one.
Set it up once:

1. `brew install 1password-cli`, then in the 1Password app turn on **Settings → Developer → Integrate with 1Password CLI** (unlocks with Touch ID).
2. In a vault `rss`, create four *API Credential* items with these custom fields.
   Secrets are *Password* fields, the rest *Text*:
   - `r2-tfstate`: `access-key-id` (Text), `secret-access-key` (Password), `endpoint` (Text, the full `https://…r2.cloudflarestorage.com` URL)
   - `r2-backups`: `access-key-id` (Text), `secret-access-key` (Password)
   - `render`: `api-key` (Password), `owner-id` (Text)
   - `cloudflare`: `api-token` (Password, the `rss-terraform` token), `account-id` (Text), `zone-id` (Text)
3. Outside the repository, create `~/.config/rss/infra.env`.
   It holds references, not secrets:

   ```sh
   AWS_ACCESS_KEY_ID=op://rss/r2-tfstate/access-key-id
   AWS_SECRET_ACCESS_KEY=op://rss/r2-tfstate/secret-access-key
   AWS_ENDPOINT_URL_S3=op://rss/r2-tfstate/endpoint
   RENDER_API_KEY=op://rss/render/api-key
   RENDER_OWNER_ID=op://rss/render/owner-id
   CLOUDFLARE_API_TOKEN=op://rss/cloudflare/api-token
   TF_VAR_cloudflare_account_id=op://rss/cloudflare/account-id
   TF_VAR_cloudflare_zone_id=op://rss/cloudflare/zone-id
   TF_VAR_backup_r2_access_key_id=op://rss/r2-backups/access-key-id
   TF_VAR_backup_r2_secret_access_key=op://rss/r2-backups/secret-access-key
   ```

4. Add an alias to `~/.zshrc`, so every command below goes through `op run`:

   ```sh
   alias tf-rss='op run --env-file ~/.config/rss/infra.env -- terraform -chdir=infra'
   ```

   Then `tf-rss init`, `tf-rss plan`, `tf-rss output -raw url`, and so on.
   The alias already includes `-chdir=infra`, so don't repeat it.
   `op read op://rss/render/owner-id` checks that a reference resolves.

## Deploys

Three workflows in `.github/workflows` run production.
They read these repository secrets (all of them secrets, IDs included): `RENDER_API_KEY`, `RENDER_OWNER_ID`, `RENDER_SERVICE_ID`, `RENDER_BACKUP_JOB_ID` (the `rss-backup` cron job's `crn-…` ID, also Terraform's `backup_job_id` output), `CLOUDFLARE_ACCOUNT_ID`, `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ZONE_ID`, `TF_STATE_ACCESS_KEY_ID`, `TF_STATE_SECRET_ACCESS_KEY`, `BACKUP_R2_ACCESS_KEY_ID` and `BACKUP_R2_SECRET_ACCESS_KEY`.

### How deploys happen

Every push to `main` that passes CI deploys; merging the PR is the approval.
CI's `image` job pushes `ghcr.io/grodier/rss:sha-<sha>` and `ghcr.io/grodier/rss-backup:sha-<sha>`, then its `deploy` job calls `deploy.yml` (environment `production`, one deploy at a time), which:

1. resolves the tag to its digest, and fails if CI pushed no image for that commit;
2. asks Render to deploy `ghcr.io/grodier/rss@<digest>`.
   Render never builds; it runs the image CI tested;
3. follows the deploy until it's live.
   Render runs `/www migrate` first; if it fails, the deploy fails within seconds and the old version keeps serving;
4. checks that `/healthcheck` reports the commit's SHA as its `version`;
5. deploys `ghcr.io/grodier/rss-backup@<digest>` to the `rss-backup` cron job, resolved the same way, and follows that deploy until it's live.

If another deploy is already running (one from the dashboard, or one a Terraform apply started), Render queues ours behind it without saying which deploy it is, so the job fails.
The queued deploy still goes ahead; follow it in Render's dashboard.

Each deploy overlaps the old and new versions for a few seconds: Render starts the new one, waits for `/healthcheck`, switches traffic, then stops the old one.

A failed deploy shows up as a failed `deploy` job in CI, and Render emails you.

### Rolling back

Run **Actions → Deploy → Run workflow** with an earlier commit's full SHA.
Its image is still in GHCR.
Rolling back doesn't undo migrations: the older code runs against the newer schema.
While pre-alpha, `migrate` refuses the rollback if the migrations differ; going back past a schema change is a reset to the older commit.

### Resetting the database

While pre-alpha, a commit that changes `migrations/*.sql` doesn't deploy: `migrate` refuses to wipe the database, the deploy fails with *"If migrate refused because the migrations changed, run the Reset production database workflow."*, and the old version keeps serving.
To deploy it:

1. Run **Actions → Reset production database → Run workflow** with the commit's full SHA and `confirm` = `wipe production`.
2. It sets `RSS_ALLOW_RESET_TO` on the service to that commit's migrations' fingerprint, then deploys the commit; its `migrate` wipes the database and rebuilds it.
   The variable stays set: it only allows a reset to the schema that has just been reached.

A reset deletes every account, yours included, and testers have to sign up again.
Getting back in is signing up with the invite link; see "Signing up".
Nothing seeds an account after a reset.

### Approving infrastructure applies

`infra.yml` runs when a pull request or a push to `main` touches `infra/**`, one run at a time:

- **Pull requests** from branches in this repository run `terraform plan` and write it to the job summary.
  Review it there like the code.
  Fork and Dependabot pull requests get no plan, since they can't read the secrets; for a Dependabot provider update, read the provider's changelog instead.
- **Pushes to `main`** run `terraform apply -auto-approve` in the `infrastructure` environment, which waits for your approval: **Actions → the run → Review deployments → Approve**.
  Approve once the PR's plan was what you meant; reject to skip the apply.

While an apply waits for approval, other `infra.yml` runs wait behind it, and GitHub keeps only the newest waiting run: an older one shows as cancelled.
Re-run it if it was an apply you still want.

### Changing the web service or the backup job

Until the provider is upgraded (a backlog item), an apply that changes `render_web_service.rss` fails with *"unable to fetch image with provided input"*: deploys pin the service to a digest, and provider v1.9.1 can't send one back ([#106](https://github.com/render-oss/terraform-provider-render/issues/106)).
Deploys pin `render_cron_job.backup` to a digest too, so expect the same of an apply that changes it.
Plans aren't affected.
Before approving such an apply:

1. On the `rss` service's (or the `rss-backup` job's) **Settings → Image** in Render's dashboard, set **Source** to `ghcr.io/grodier/rss:main` (or `ghcr.io/grodier/rss-backup:main`) and save.
   CI pushes `:main` for every commit on `main`, so this redeploys the image that's running; wait until it's live.
2. Approve the apply (or re-run it if it already failed).

The next deploy pins the digest again.

## Backups and restore

Two layers, both for the database only:

- **Point-in-time recovery** by Render, for the last 3 days (Hobby workspace).
- **A nightly dump** to the R2 bucket `rss-backups`, at another provider, kept 35 days.
  The cron job `rss-backup` runs `deploy/backup/backup.sh` at 08:00 UTC: `pg_dump --format=custom --no-owner`, streamed to `rss-<UTC date and time>.dump` (for example `rss-2026-10-14T0800Z.dump`).
  A bucket lifecycle rule deletes dumps 35 days old.
  A failed run deletes what it uploaded, exits non-zero, and Render emails you.

Terraform sets the bucket's lifecycle rules but doesn't own the bucket, which was created by hand (#208) like `rss-tfstate`.

**The backup image.** CI builds `ghcr.io/grodier/rss-backup` from `deploy/backup` and pushes it on every push to `main`.
The package must be public, like `rss`, or Render can't pull it.
Render pulls a cron job's image only when the job is deployed, so every app deploy also deploys the job by digest (step 5 of "How deploys happen"), and a rollback rolls it back too.
Its Postgres major version must stay that of the database.

### Listing and downloading dumps

In Cloudflare's dashboard, **R2 Object Storage → `rss-backups`** lists the dumps and downloads one.
Or with the AWS CLI and the `rss-backups` token from your password manager:

```sh
export AWS_ACCESS_KEY_ID=...        # the rss-backups R2 token's Access Key ID
export AWS_SECRET_ACCESS_KEY=...    # the rss-backups R2 token's Secret Access Key
export AWS_DEFAULT_REGION=auto
R2=https://<account ID>.r2.cloudflarestorage.com

aws s3 ls s3://rss-backups --endpoint-url "$R2"
aws s3 cp s3://rss-backups/rss-2026-10-14T0800Z.dump . --endpoint-url "$R2"
```

### Restoring into the local database

A dump restores cleanly only into a database with the same migrations, so check out the commit production ran when the dump was taken, then:

1. `make db/reset`
2. `pg_restore --clean --if-exists --no-owner --no-privileges -d "$RSS_DB_DSN" rss-….dump` (with PostgreSQL 17's `pg_restore`).
   `--no-privileges` skips the dump's grants to Render's database role, which doesn't exist locally.

### Restoring into production

- **Within 3 days:** point-in-time recovery, from the database's **Recovery** page in Render's dashboard → **Restore Database**, with a time at least ten minutes ago.
  It doesn't restore in place: Render creates a *new* database with the data as it was at that time ([docs](https://render.com/docs/postgresql-backups)), and the service has to be pointed at it.
  Since Terraform owns `rss-db` and the service's `RSS_DB_DSN`, do this only for a real emergency, and bring Terraform in line afterwards.
- **Older:** restore a dump into `rss-db` with a Render one-off job on `rss-backup`, which has `pg_restore`, the AWS CLI and the same environment.
  The database has no external access, so the restore must run inside Render.
  As with a local restore, the deployed migrations must match the dump's; if they don't, deploy the commit that matches first.

  ```sh
  curl -sS -X POST "https://api.render.com/v1/services/<rss-backup's ID, crn-…>/jobs" \
    -H "Authorization: Bearer $RENDER_API_KEY" -H 'Content-Type: application/json' \
    -d '{"startCommand": "sh -c '"'"'set -euo pipefail; aws s3 cp s3://$R2_BUCKET/rss-2026-10-14T0800Z.dump - --endpoint-url $R2_ENDPOINT | pg_restore --clean --if-exists --no-owner -d $DATABASE_URL'"'"'"}'
  ```

  The job's logs are on `rss-backup`'s **One-off Jobs** page.
  `--clean` replaces every table, so anything written since the dump is lost.

### Test restores

- **2026-10-14** (#217): a dump from a manual run of `rss-backup`, restored into the local database at the commit production ran (`68cadf3`).
  The app ran locally against it, the production account logged in, and its subscriptions were there.

## Signing up

Signup is open only to people with the shared invite code, and only while `RSS_INVITE_CODE` is set; without it, `/signup` returns 404.
No page links to `/signup`, so share the link itself.
Terraform ignores the variable, so none of this needs a PR or an apply:

1. Generate a code (at least 16 characters): `openssl rand -hex 16`.
2. On the `rss` service's **Environment** page in Render's dashboard, set `RSS_INVITE_CODE` to it and save (Render restarts the service).
3. Share `<url>/signup?invite=<code>`.
   It opens the signup page with the code filled in; the code can also be typed into the "Invite code" field.
   Each person chooses their own password.

Someone who forgets their password resets it themselves with "Forgot password?" on the login page, which emails them a link; nothing needs changing in the database.

To rotate the code, edit the variable; to turn signup off, delete it.
The code may appear in the request logs, which log the URI.

## Domain and email

The app is served at `https://rss.georgerodier.com` and sends its email from it, through Resend over SMTP.
Why: "Accounts" in [`docs/decisions.md`](../docs/decisions.md).
What lives where:

- **DNS records** in `domain.tf`, in the `georgerodier.com` zone: the app's CNAME (DNS-only), and Resend's records (CNAMEs on `send.rss` and `rsend.rss`, the DKIM key on `resend._domainkey.rss`), with the names and values Resend's dashboard showed.
  The service's `custom_domains` makes Render verify the domain and issue its certificate.
- **The Resend domain** `rss.georgerodier.com`, in region us-east-1, and its API key, which is the SMTP password (`RSS_SMTP_PASSWORD`).
  The other SMTP settings are in `main.tf`.
- **Support mail** to `rss-support@georgerodier.com`, an alias in Google, which handles the `georgerodier.com` mail.
  It isn't on `rss.georgerodier.com`, since the app's CNAME can't share its name with MX records.
- **DMARC**: no record on `_dmarc.rss`, so mail from `rss.georgerodier.com` falls back to the `p=none` record on `_dmarc.georgerodier.com`.
  Cloudflare's **DMARC Management** (**`georgerodier.com` → Email → DMARC Management**) created it outside Terraform and shows its reports.
  It doesn't support subdomains.

Resend's free tier allows 100 emails a day and 3,000 a month.
`www send-test-email` (#259) checks deliverability.

### Setting up

Done once, in dashboards, for #254:

1. **Cloudflare API token:** on the account-owned `rss-terraform` token, add a policy for the zone `georgerodier.com` with **DNS: Edit**.
   Add the zone's ID (the zone's **Overview** page) as the repository secret `CLOUDFLARE_ZONE_ID`, and as `zone-id` in 1Password.
2. **Resend:** add the domain `rss.georgerodier.com` in region us-east-1 with **Manual setup**: Terraform owns the records, so don't let Resend configure Cloudflare, and don't add its suggested `_dmarc` record.
   Leave click and open tracking off, since they would rewrite the links in emails.
3. **Support address:** add `rss-support@georgerodier.com` as an alias of your Google user.
4. **DMARC Management:** enable it for the zone; it adds the `_dmarc.georgerodier.com` record.
5. **Apply** the PR.
   Then, in Resend, verify the domain, and check that Render shows the custom domain as verified, with a certificate.
6. **The SMTP password:** in Resend, create an API key with **Sending access** for `rss.georgerodier.com` only (Resend offers only "All domains" until the domain is verified).
   Save it in 1Password, then set it as `RSS_SMTP_PASSWORD` on the `rss` service's **Environment** page.
   Only once the apply has run: before it, Terraform didn't ignore the variable, and an apply would have deleted it.

### Rotating the SMTP password

1. In Resend, create a new API key as in step 6 above.
2. Set `RSS_SMTP_PASSWORD` to it on the service's **Environment** page (saving redeploys), and update 1Password.
3. Once the deploy is live, delete the old key in Resend.

Render blocks outbound SMTP only on free instances; `rss` is on Starter.
If connections to port 465 time out anyway, Resend also listens on 2465 (`RSS_SMTP_PORT`).

## Checking the client IP

The service sets `RSS_PROXY_HOPS=2`: requests reach the app through Cloudflare and then Render's proxy, and each appends one `X-Forwarded-For` entry, so a request arrives with `<whatever the client sent>, <client>, <Cloudflare server>`.
To check that the app sees visitors' real addresses, run:

```sh
curl -4 -H 'X-Forwarded-For: 203.0.113.9' https://rss.georgerodier.com/login >/dev/null
curl -4 https://api.ipify.org    # your public IPv4 address
```

In Render's logs, the `ip` of the JSON request log line for `/login` must be **your own public IP**.
Anything else means the hop count is wrong:

- `203.0.113.9`: one hop too many; the app is trusting what the client sent.
- A Cloudflare address (such as `172.64.0.0/13` or `104.16.0.0/13`): one hop too few.
  The app doesn't warn about this one, because the address is public.
- A private address, with the warning `client IP from X-Forwarded-For is not a public address; check -proxy-hops`: one hop too few, and the extra hop is inside Render.

To find the right count, change `RSS_PROXY_HOPS` on the service's **Environment** page (saving redeploys) and repeat the check, then set the value that works in `main.tf` and apply.
If no value works, set it to `0` and stop: every visitor then shares one login limit, but nobody can forge their address.

## Facts recorded after setup

Filled in by #214, checked on 2026-10-13.

- **Log retention:** 7 days on the Hobby workspace ([logging docs](https://render.com/docs/logging), [pricing](https://render.com/pricing#plans)).
  That bounds how far back an incident can be investigated, and how long the client IPs in the request logs are kept.
  Logs past it are gone even after upgrading; keeping them longer needs a log stream.
- **Cost:** Render's projection for October 2026 was $10.67 for the web service and database, created on 13 October, so a partial month.
