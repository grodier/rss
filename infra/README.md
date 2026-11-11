# infra

Terraform for production on Render: one Postgres 17 database (`rss-db`), two web services, the app (`rss`, running `ghcr.io/grodier/rss`) and the admin (`rss-admin`, running `ghcr.io/grodier/rss-admin`, behind Cloudflare Access; see "The admin"), and one cron job (`rss-backup`) that dumps the database to the Cloudflare R2 bucket `rss-backups` every night, all in Virginia.
State lives in the R2 bucket `rss-tfstate`.
Why Render and this setup: "Deployment" in [`docs/decisions.md`](../docs/decisions.md).

Terraform doesn't own these things that change outside it, and ignores them:

- **The images** of the web services and the backup cron job. Deploys set them by digest; `apply` never rolls a deploy back.
- **`RSS_DEBUG_EMAILS`**, set in Render's dashboard: the accounts allowed debug mode (see "Debug mode" in [`docs/features.md`](../docs/features.md)).
- **`RSS_SMTP_PASSWORD`**, set in Render's dashboard on both services (see "Domain and email").
- **`RSS_SECRET_KEY`** on `rss-admin`, set in Render's dashboard (see "The admin").
- **Edge caching**, set in Render's dashboard (see "Edge caching").
- **The admin's `onrender.com` subdomain, disabled** in Render's dashboard: the provider has no attribute for it (see "The admin").

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
export TF_VAR_cloudflare_app_zone_id=...       # the paperround.app zone's ID
export TF_VAR_admin_access_email=...           # the email Cloudflare Access lets into the admin
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
   - `cloudflare`: `api-token` (Password, the `rss-terraform` token), `account-id` (Text), `zone-id` (Text, `georgerodier.com`), `app-zone-id` (Text, `paperround.app`), `admin-access-email` (Text), `team-name` (Text, the Zero Trust team, for reference)
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
   TF_VAR_cloudflare_app_zone_id=op://rss/cloudflare/app-zone-id
   TF_VAR_admin_access_email=op://rss/cloudflare/admin-access-email
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
They read these repository secrets (all of them secrets, IDs included): `RENDER_API_KEY`, `RENDER_OWNER_ID`, `RENDER_SERVICE_ID`, `RENDER_ADMIN_SERVICE_ID` (the `rss-admin` service's `srv-…` ID, also Terraform's `admin_service_id` output), `RENDER_BACKUP_JOB_ID` (the `rss-backup` cron job's `crn-…` ID, also Terraform's `backup_job_id` output), `ADMIN_ACCESS_EMAIL` (the email Cloudflare Access lets into the admin), `CLOUDFLARE_ACCOUNT_ID`, `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ZONE_ID`, `CLOUDFLARE_APP_ZONE_ID`, `TF_STATE_ACCESS_KEY_ID`, `TF_STATE_SECRET_ACCESS_KEY`, `BACKUP_R2_ACCESS_KEY_ID` and `BACKUP_R2_SECRET_ACCESS_KEY`.

### How deploys happen

Every push to `main` that passes CI deploys; merging the PR is the approval.
CI's `image` job pushes `ghcr.io/grodier/rss:sha-<sha>`, `ghcr.io/grodier/rss-admin:sha-<sha>` (the Dockerfile's `admin` stage, from the same build) and `ghcr.io/grodier/rss-backup:sha-<sha>`, then its `deploy` job calls `deploy.yml` (environment `production`, one deploy at a time), which:

1. resolves the tag to its digest, and fails if CI pushed no image for that commit;
2. asks Render to deploy `ghcr.io/grodier/rss@<digest>`.
   Render never builds; it runs the image CI tested;
3. follows the deploy until it's live.
   Render runs `/www migrate -seed` first, which applies the migrations, then queues lookups of the seed list's sites not found yet (`www seed`; their keys are in its log); if it fails, the deploy fails within seconds and the old version keeps serving;
4. checks that `/healthcheck` reports the commit's SHA as its `version`;
5. deploys `ghcr.io/grodier/rss-backup@<digest>` to the `rss-backup` cron job, resolved the same way, and follows that deploy until it's live;
6. deploys `ghcr.io/grodier/rss-admin@<digest>` to `rss-admin` the same way, and checks that `https://admin.paperround.app/healthcheck` reports the SHA (Access lets `/healthcheck` through).
   It's last, so a failed admin deploy leaves `rss` and the backup job deployed and doesn't roll them back; rerun the job once it's fixed.

If another deploy is already running (one from the dashboard, or one a Terraform apply started), Render queues ours behind it without saying which deploy it is, so the job fails.
The queued deploy still goes ahead; follow it in Render's dashboard.

Each deploy overlaps the old and new versions for a few seconds: Render starts the new one, waits for `/healthcheck`, switches traffic, then stops the old one.

A failed deploy shows up as a failed `deploy` job in CI, and Render emails you.

### Rolling back

Run **Actions → Deploy → Run workflow** with an earlier commit's full SHA.
Its image is still in GHCR.
Rolling back doesn't undo migrations: the older image runs against the newer schema, and its `migrate` has nothing to apply.
Every migration works with the version before it (see "Prototype stage" in [`CLAUDE.md`](../CLAUDE.md)), so rolling back one deploy is safe; going further back past a migration that dropped something the older code uses is not.
Commits from before #573 have no `rss-admin` image, so rolling back to one fails at the last step, after `rss` and the backup job have been rolled back; the admin keeps running its newer image.

### Approving infrastructure applies

`infra.yml` runs when a pull request or a push to `main` touches `infra/**` other than Markdown:

- **Pull requests** from branches in this repository run `terraform plan` and write it to the job summary.
  Review it there like the code.
  Fork and Dependabot pull requests get no plan, since they can't read the secrets; for a Dependabot provider update, read the provider's changelog instead.
- **Pushes to `main`** run `terraform apply -auto-approve` in the `infrastructure` environment, which waits for your approval: **Actions → the run → Review deployments → Approve**.
  Approve once the PR's plan was what you meant; reject to skip the apply.

While an apply waits for approval, later applies wait behind it, and GitHub keeps only the newest waiting one: an older one shows as cancelled.
Re-run it if it was an apply you still want.
Plans don't wait behind applies; each pull request gets one plan at a time, and Terraform's state lock makes a plan wait (up to 5 minutes) while an apply is running.

### A stale state lock

A plan or apply that fails with `Error acquiring the state lock` while nothing else is running is blocked by a lock a killed run left behind: Terraform releases its lock when it stops, but not when its job is killed.
The workflows interrupt Terraform before their jobs' time limits (`timeout --signal=INT`), so it can stop cleanly; a lock can still be left by a run cancelled by hand or a runner that died.

1. Check no plan or apply is running or waiting (Actions → Infrastructure).
2. Copy the lock's `ID` from the failed run's "Lock Info".
3. **Actions → Unlock Terraform state → Run workflow**, paste the ID, then approve it in the `infrastructure` environment (**Review deployments → Approve**); this works from the GitHub app or a phone's browser.
4. Re-run the failed plan or apply.

With the state's credentials locally, `terraform -chdir=infra force-unlock <ID>` does the same.

### Changing the web service or the backup job

Until the provider is upgraded (a backlog item), an apply that changes `render_web_service.rss` (or `render_web_service.admin`) fails with *"unable to fetch image with provided input"*: deploys pin the service to a digest, and provider v1.9.1 can't send one back ([#106](https://github.com/render-oss/terraform-provider-render/issues/106)).
Deploys pin `render_cron_job.backup` to a digest too, so expect the same of an apply that changes it.
Plans aren't affected.
Before approving such an apply:

1. On the `rss` service's (or `rss-admin`'s, or the `rss-backup` job's) **Settings → Image** in Render's dashboard, set **Source** to `ghcr.io/grodier/rss:main` (or `ghcr.io/grodier/rss-admin:main`, or `ghcr.io/grodier/rss-backup:main`) and save.
   CI pushes `:main` for every commit on `main`, so this redeploys the image that's running; wait until it's live.
2. Approve the apply (or re-run it if it already failed).

The next deploy pins the digest again.

## The admin

The admin site (`cmd/admin`) runs as its own web service, `rss-admin`, at `https://admin.paperround.app`, with three layers in front of it (why: "Admin" in [`docs/decisions.md`](../docs/decisions.md)):

- **Cloudflare Access** (`access.tf`), on the Zero Trust Free plan (team `bold-frog-19d2` in 1Password): only `ADMIN_ACCESS_EMAIL`, the owner's Cloudflare login, gets in, by signing in to Cloudflare (**Sign in with Cloudflare**, with the account's second factor), for 24 hours.
  That's the Cloudflare identity provider, the only login method, which new Zero Trust organizations get by default, limited to the account's members; no one-time PIN is set up.
  `/healthcheck` is its own Access application that lets everyone through, so the deploy workflow can check the version; it reports what `paperround.app/healthcheck` does.
  Access applies only to a proxied DNS record, so the admin's is the zone's only proxied one, and the zone's SSL/TLS mode is **Full (strict)** (with **Flexible**, Cloudflare would call Render over HTTP and Render's redirect to HTTPS would loop).
- **No `onrender.com` URL**: the service's Render subdomain is disabled, so the only way in is through Access.
  Terraform's provider has no attribute for it, so it's set in Render's dashboard.
- **The admin's own login**, a password and a TOTP code, which is the real gate.

**Its image** is the Dockerfile's `admin` stage, built in the same run as `rss`'s: Alpine, with `/admin` as the entrypoint and a non-root `admin` user with a shell, so `admin` commands run from the service's **Shell** in Render's dashboard.
The `rss` image stays distroless, with no shell.
The package `ghcr.io/grodier/rss-admin` must be public, like `rss`, or Render can't pull it.

**Its environment:** the database (at most 3 connections), its own URL in `RSS_BASE_URL`, the reader's in `RSS_APP_URL` (for invite links), and the mail settings `rss` has, all in `main.tf`; `RSS_SECRET_KEY` and `RSS_SMTP_PASSWORD` are set in the dashboard.
It runs none of `www`'s background workers.

### Deploying the admin

Done once, for #573.
Before the PR:

1. **Cloudflare:** turn on Zero Trust on the account (Free plan), and on the account-owned `rss-terraform` token add **Access: Edit** (under Cloudflare One / Zero Trust, "Access", which covers applications and policies).
2. **Secrets:** add the repository secret `ADMIN_ACCESS_EMAIL`, and `admin-access-email` to 1Password (see "With 1Password").
3. **The key:** generate `RSS_SECRET_KEY` straight into 1Password (`op item create --vault rss --category "API Credential" --title rss-admin "secret-key[password]=$(openssl rand -hex 32)"`).

After merging, in order:

1. **The image:** once CI's `image` job has pushed `ghcr.io/grodier/rss-admin`, make the package public (**GitHub → Packages → rss-admin → Package settings → Change visibility**).
   The `deploy` job's last step fails this first time, since `RENDER_ADMIN_SERVICE_ID` isn't set yet; `rss` and the backup job are deployed before it.
2. **Approve the apply.** It creates `rss-admin`, its DNS record (DNS-only for now) and the Access applications.
   The service's first deploy fails, since `RSS_SECRET_KEY` isn't set: `admin` refuses to start in production without it.
3. **Set the secrets** on `rss-admin`'s **Environment** page: `RSS_SECRET_KEY` from 1Password and `RSS_SMTP_PASSWORD` (the same Resend key as `rss`'s), then save, which redeploys.
4. **Check the domain:** on `rss-admin`'s **Settings → Custom Domains**, wait until `admin.paperround.app` is verified, with a certificate.
   Render asks for DNS-only records until then.
5. **Disable the Render subdomain:** on the same page, set **Render Subdomain** to **Disabled**.
6. **Add `RENDER_ADMIN_SERVICE_ID`** as a repository secret (`terraform output -raw admin_service_id`, or the `srv-…` ID in the service's URL in the dashboard), and rerun the failed `deploy` job.
7. **Proxy the record:** a follow-up PR (#573's second) sets `proxied = true` on `cloudflare_dns_record.admin` and `RSS_PROXY_HOPS` to `3`.
   Before approving its apply, set the `paperround.app` zone's **SSL/TLS → Overview** to **Full (strict)**, and follow "Changing the web service or the backup job" for `rss-admin` (the apply changes its environment).
8. **Check Access** (below), then open `rss-admin`'s **Shell** and run `/admin -version`, which should print the deployed SHA; then enroll (below), then check the client IP ("Checking the client IP").

**Checking that Access can't be bypassed:**

```sh
curl -sI https://admin.paperround.app/login | head -3       # 302 to bold-frog-19d2.cloudflareaccess.com
curl -s https://admin.paperround.app/healthcheck            # the JSON, with the deployed SHA
curl -sI <the service's onrender.com URL> | head -1          # 404 (the URL is on the service's dashboard page)
```

In a browser, `https://admin.paperround.app` shows Access's page with **Sign in with Cloudflare**; after your Cloudflare login and second factor, it shows the admin's own login.

### Enrolling an admin

The admin site has no admin until one is enrolled, and nothing seeds one.
Open the `rss-admin` service's **Shell** in Render's dashboard and run:

```sh
/admin enroll -email you@example.com
```

It reads `RSS_DB_DSN` and `RSS_SECRET_KEY` from the service's environment.
Type the password on the first line (the shell shows it, so clear the screen afterwards), scan the QR code (or add the `otpauth://` URI) with an authenticator app, type the code the app shows, and store the 10 recovery codes it prints in the password manager: they're shown once.
For a lost phone or password, run the same with `-reset`, which replaces the admin's password, secret and recovery codes and logs every admin out, so a session on a lost device ends too.

### Rotating `RSS_SECRET_KEY`

The key seals every admin's TOTP secret, so a new key makes every secret unreadable and every admin enrolls again.

1. Replace the key in 1Password: `op item edit rss-admin --vault rss "secret-key[password]=$(openssl rand -hex 32)"`.
2. Set it as `RSS_SECRET_KEY` on `rss-admin`'s **Environment** page (saving redeploys).
3. From the service's **Shell**, run `/admin enroll -email <admin> -reset` for each admin.

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

Signup needs a single-use invite; no page links to `/signup`, so each person gets their own link.
Until the admin's pages send invites (#574), make them from the `rss-admin` service's **Shell** in Render's dashboard (its `RSS_APP_URL` is `https://paperround.app`, for the links):

```sh
/admin invite create -email someone@example.com   # bound to their confirmed waitlist entry
/admin invite create -note "Grace, by hand"       # open: they type their own email
/admin invite list
/admin invite revoke <id>
```

`create` prints `https://paperround.app/signup?invite=<token>`; send it to the person yourself.
A bound invite fills in their name and address, and their account starts confirmed; an open one works for any address, which they confirm afterwards.
Each works once and expires after 14 days (`-expires` changes that); the README's "`admin invite`" has the details.
Each person chooses their own password.

Someone who forgets their password resets it themselves with "Forgot password?" on the login page, which emails them a link; nothing needs changing in the database.

Invite tokens are in the signup page's URL, so they may appear in the request logs, which log the URI; a used, expired or revoked one opens nothing.

**Owner step after #569 deploys:** delete `RSS_INVITE_CODE` from the `rss` service's **Environment** page in Render's dashboard; nothing reads it anymore.

## Domain and email

The app is served at `https://paperround.app` and sends its email from it, through Resend over SMTP.
`www.paperround.app` redirects to it.
Why: "Accounts" in [`docs/decisions.md`](../docs/decisions.md).
What lives where:

- **DNS records** in `domain.tf`, in the `paperround.app` zone: the app's CNAMEs on the apex (which Cloudflare flattens) and `www`, DNS-only, the admin's on `admin` (proxied, for Access; see "The admin"), and Resend's records (CNAMEs on `send` and `rsend`, the DKIM key on `resend._domainkey`), with the names and values Resend's dashboard showed.
  The service's `custom_domains` makes Render verify the domain and issue its certificates; Render adds `www` with the apex and redirects it.
- **The old host** `rss.georgerodier.com`: its CNAME stays in `domain.tf`, in the `georgerodier.com` zone, and it stays a custom domain on the service, so old links and bookmarks keep working.
  The app redirects it, and the service's `onrender.com` URL, to `https://paperround.app` with a 308 (`redirectToCanonicalHost`), except `/healthcheck`.
- **The Resend domain** `paperround.app`, in region us-east-1, and its API key, which is the SMTP password (`RSS_SMTP_PASSWORD`).
  The other SMTP settings are in `main.tf`.
- **Support mail** to `rss-support@georgerodier.com`, an alias in Google, which handles the `georgerodier.com` mail.
  It's the Reply-To of every email; `paperround.app` has no MX records, so it receives no mail.
- **DMARC**: the `p=none` record on `_dmarc.paperround.app`, which Cloudflare's **DMARC Management** (**`paperround.app` → Email → DMARC Management**) created outside Terraform, and whose reports it shows.

Resend's free tier allows 100 emails a day and 3,000 a month.
`admin send-test-email` (#259, moved to `admin` in #568) checks deliverability, from the `rss-admin` service's **Shell**, with the app's URL as `-base-url` (see the README).

### Setting up

Done once, in dashboards, for #389:

1. **Cloudflare API token:** on the account-owned `rss-terraform` token, add a policy for the zone `paperround.app` with **DNS: Edit**.
   Add the zone's ID (the zone's **Overview** page) as the repository secret `CLOUDFLARE_APP_ZONE_ID`, and as `app-zone-id` in 1Password.
2. **Resend:** add the domain `paperround.app` in region us-east-1 with **Manual setup**: Terraform owns the records, so don't let Resend configure Cloudflare, and don't add its suggested `_dmarc` record.
   Leave click and open tracking off, since they would rewrite the links in emails.
3. **DMARC Management:** enable it for the zone; it adds the `_dmarc.paperround.app` record.
4. **Apply** the PR, after following "Changing the web service or the backup job" (the apply changes the service).
   Then, in Resend, verify the domain, and check that Render shows `paperround.app` and `www.paperround.app` as verified, with certificates.
5. **The SMTP password:** in Resend, create an API key with **Sending access** for `paperround.app` only (Resend offers only "All domains" until the domain is verified), as in "Rotating the SMTP password".
   Until it's set, sending fails if the old key only sends from `rss.georgerodier.com`.
6. **The old Resend domain:** once email arrives from `paperround.app`, delete `rss.georgerodier.com` in Resend; the apply removed its records.

### Rotating the SMTP password

1. In Resend, create a new API key with **Sending access** for `paperround.app` only.
2. Set `RSS_SMTP_PASSWORD` to it on the service's **Environment** page (saving redeploys), and update 1Password.
3. Once the deploy is live, delete the old key in Resend.

Render blocks outbound SMTP only on free instances; `rss` is on Starter.
If connections to port 465 time out anyway, Resend also listens on 2465 (`RSS_SMTP_PORT`).

## Edge caching

Render puts Cloudflare in front of the `rss` service and can cache its static files there at no charge (#541, [Render's docs](https://render.com/docs/web-service-caching)).
The setting is **Cacheable file types: Common static files**, under the service's **Settings → Edge Caching** in Render's dashboard.
It isn't in Terraform: the `render_web_service` resource in the pinned `render-oss/render` provider (1.9.1) has no attribute for it, so check the dashboard after rebuilding the service.
Not "All files": logged-out pages send no `Cache-Control`, so Render would cache them for 2 hours.
`/sites/{id}/icon` has no file extension and isn't cached at the edge; browsers cache it.

Check it with a current versioned asset URL from a page's source:

```sh
curl -sI "https://paperround.app/<versioned asset URL>" | grep -i 'cf-cache-status\|cache-control'
```

The first request after a deploy says `cf-cache-status: MISS` and the next `HIT`, with `cache-control: public, max-age=31536000, immutable`: every successful deploy purges the edge cache.
Pages (`/login`, or a logged-in page) say `DYNAMIC` or `BYPASS`, never `HIT`.
`curl -sI -H 'Accept-Encoding: br, gzip' <the CSS URL>` should answer `content-encoding: br`.
DevTools' Network panel, Protocol column, shows `h3` (HTTP/3) on a reload; the edge serves HTTP/2 and HTTP/3, and the app serves neither itself.

## Checking the client IP

The service sets `RSS_PROXY_HOPS=2`: requests reach the app through Cloudflare and then Render's proxy, and each appends one `X-Forwarded-For` entry, so a request arrives with `<whatever the client sent>, <client>, <Cloudflare server>`.
To check that the app sees visitors' real addresses, run:

```sh
curl -4 -H 'X-Forwarded-For: 203.0.113.9' https://paperround.app/login >/dev/null
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

**The admin** has one more hop once its record is proxied: the `paperround.app` zone's Cloudflare, then Render's, then Render's proxy, so its `RSS_PROXY_HOPS` is `3`.
Check it by logging in to the admin (after Access) and opening its audit log (`/audit`): the `login` row's IP must be your own public IP (`curl -4 https://api.ipify.org`, or `-6` if your browser used IPv6).
The same mistakes show the same way, except that the forged-header test can't be sent from a browser.

## Facts recorded after setup

Filled in by #214, checked on 2026-10-13.

- **Log retention:** 7 days on the Hobby workspace ([logging docs](https://render.com/docs/logging), [pricing](https://render.com/pricing#plans)).
  That bounds how far back an incident can be investigated, and how long the client IPs in the request logs are kept.
  Logs past it are gone even after upgrading; keeping them longer needs a log stream.
- **Cost:** Render's projection for October 2026 was $10.67 for the web service and database, created on 13 October, so a partial month.
- **Uptime monitor:** UptimeRobot (free plan), keyword `available` on `https://paperround.app/healthcheck` every 5 minutes (the keyword also stops matching when the database doesn't answer: `degraded`), alerts by email to the owner. Set up on 2026-10-30 (#391).
  It lives in UptimeRobot's dashboard, not in Terraform, and watches from outside Render.
  The login is in 1Password.
