# infra

Terraform for production on Render: one Postgres 17 database (`rss-db`) and one web
service (`rss`) running `ghcr.io/grodier/rss`, both in Virginia. State lives in the
Cloudflare R2 bucket `rss-tfstate`. Why Render and this setup: "Deployment" in
[`docs/decisions.md`](../docs/decisions.md).

Terraform doesn't own two things that change outside it, and ignores them:

- **The image.** Deploys set it by digest; `apply` never rolls a deploy back.
- **`RSS_ALLOW_RESET_TO`**, set by the "Reset production database" workflow.

## Prerequisites

The accounts, buckets, tokens and GitHub settings from #208, and Terraform 1.10 or later.

## Running Terraform locally

Since the first apply (#214), applies run only from GitHub Actions (see "Approving
infrastructure applies"). Locally, run `plan` and `output`, never `apply`. Take every value from your password manager, never from a file in the repository:

```sh
export AWS_ACCESS_KEY_ID=...        # the rss-tfstate R2 token's Access Key ID
export AWS_SECRET_ACCESS_KEY=...    # the rss-tfstate R2 token's Secret Access Key
export AWS_ENDPOINT_URL_S3=https://<account ID>.r2.cloudflarestorage.com
export RENDER_API_KEY=...
export RENDER_OWNER_ID=...          # starts with tea- or usr-
```

Then, from the repository root:

```sh
terraform -chdir=infra init
terraform -chdir=infra plan
```

In a plan, nothing should be destroyed or replaced unless you meant it, and no secret
should appear in plain text. The database has `prevent_destroy`.

`terraform -chdir=infra output -raw url` prints the service's URL, and `output -raw
service_id` its ID.

### With 1Password

`op run` hands the values to one Terraform command without exporting them into your
shell, and masks them if Terraform prints one. Set it up once:

1. `brew install 1password-cli`, then in the 1Password app turn on **Settings →
   Developer → Integrate with 1Password CLI** (unlocks with Touch ID).
2. In a vault `rss`, create two *API Credential* items with these custom fields.
   Secrets are *Password* fields, the rest *Text*:
   - `r2-tfstate`: `access-key-id` (Text), `secret-access-key` (Password),
     `endpoint` (Text, the full `https://…r2.cloudflarestorage.com` URL)
   - `render`: `api-key` (Password), `owner-id` (Text)
3. Outside the repository, create `~/.config/rss/infra.env`. It holds references, not
   secrets:

   ```sh
   AWS_ACCESS_KEY_ID=op://rss/r2-tfstate/access-key-id
   AWS_SECRET_ACCESS_KEY=op://rss/r2-tfstate/secret-access-key
   AWS_ENDPOINT_URL_S3=op://rss/r2-tfstate/endpoint
   RENDER_API_KEY=op://rss/render/api-key
   RENDER_OWNER_ID=op://rss/render/owner-id
   ```

4. Add an alias to `~/.zshrc`, so every command below goes through `op run`:

   ```sh
   alias tf-rss='op run --env-file ~/.config/rss/infra.env -- terraform -chdir=infra'
   ```

   Then `tf-rss init`, `tf-rss plan`, `tf-rss output -raw url`, and so on. The alias already includes `-chdir=infra`, so don't repeat it. `op read
   op://rss/render/owner-id` checks that a reference resolves.

## Deploys

Three workflows in `.github/workflows` run production. They read these repository
secrets (all of them secrets, IDs included): `RENDER_API_KEY`, `RENDER_OWNER_ID`,
`RENDER_SERVICE_ID`, `CLOUDFLARE_ACCOUNT_ID`, `CLOUDFLARE_API_TOKEN`,
`TF_STATE_ACCESS_KEY_ID`, `TF_STATE_SECRET_ACCESS_KEY`, `BACKUP_R2_ACCESS_KEY_ID` and
`BACKUP_R2_SECRET_ACCESS_KEY`.

### How deploys happen

Every push to `main` that passes CI deploys; merging the PR is the approval. CI's `image`
job pushes `ghcr.io/grodier/rss:sha-<sha>`, then its `deploy` job calls `deploy.yml`
(environment `production`, one deploy at a time), which:

1. resolves the tag to its digest, and fails if CI pushed no image for that commit;
2. waits for any deploy already running on the service, then asks Render to deploy
   `ghcr.io/grodier/rss@<digest>`. Render never builds; it runs the image CI tested;
3. waits until the deploy is live. Render runs `/www migrate` first; if it fails, the
   deploy fails and the old version keeps serving;
4. checks that `/healthcheck` reports the commit's SHA as its `version`.

Each deploy overlaps the old and new versions for a few seconds: Render starts the new
one, waits for `/healthcheck`, switches traffic, then stops the old one.

A failed deploy shows up as a failed `deploy` job in CI, and Render emails you.

### Rolling back

Run **Actions → Deploy → Run workflow** with an earlier commit's full SHA. Its image is
still in GHCR. Rolling back doesn't undo migrations: the older code runs against the
newer schema. While pre-alpha, `migrate` refuses the rollback if the migrations differ;
going back past a schema change is a reset to the older commit.

### Resetting the database

While pre-alpha, a commit that changes `migrations/*.sql` doesn't deploy: `migrate`
refuses to wipe the database, the deploy fails with *"If migrate refused because the
migrations changed, run the Reset production database workflow."*, and the old version
keeps serving. To deploy it:

1. Run **Actions → Reset production database → Run workflow** with the commit's full SHA
   and `confirm` = `wipe production`.
2. It sets `RSS_ALLOW_RESET_TO` on the service to that commit's migrations' fingerprint,
   then deploys the commit; its `migrate` wipes the database and rebuilds it. The
   variable stays set: it only allows a reset to the schema that has just been reached.

A reset deletes every account, yours included, and testers have to sign up again.
Getting back in is signing up with the invite link (#226, which updates this section
when it lands). **Until then, don't reset production:** applies run only from GitHub
Actions, so the "First account" local apply no longer applies, and there's no other way
back in.

### Approving infrastructure applies

`infra.yml` runs when a pull request or a push to `main` touches `infra/**`, one run at a
time:

- **Pull requests** from branches in this repository run `terraform plan` and write it to
  the job summary. Review it there like the code. Fork and Dependabot pull requests get
  no plan, since they can't read the secrets; for a Dependabot provider update, read the
  provider's changelog instead.
- **Pushes to `main`** run `terraform apply -auto-approve` in the `infrastructure`
  environment, which waits for your approval: **Actions → the run → Review deployments →
  Approve**. Approve once the PR's plan was what you meant; reject to skip the apply.

While an apply waits for approval, other `infra.yml` runs wait behind it, and GitHub keeps
only the newest waiting run: an older one shows as cancelled. Re-run it if it was an
apply you still want.

## First account

This describes the first apply in #214 only. Applies now run only from GitHub Actions, so
it no longer works; after a reset, see "Resetting the database".

Signup is off. To create an account, turn it on briefly:

```sh
terraform -chdir=infra apply -var signup=true
# sign up at <url>/signup
terraform -chdir=infra apply    # signup off again; /signup returns 404
```

The shared invite code (#226) later replaces this.

## Checking the client IP

The service sets `RSS_PROXY_HOPS=2`: requests reach the app through Cloudflare and then
Render's proxy, and each appends one `X-Forwarded-For` entry, so a request arrives with
`<whatever the client sent>, <client>, <Cloudflare server>`. To check that the app sees
visitors' real addresses, run:

```sh
curl -4 -H 'X-Forwarded-For: 203.0.113.9' "$URL/login" >/dev/null
curl -4 https://api.ipify.org    # your public IPv4 address
```

In Render's logs, the `ip` of the JSON request log line for `/login` must be **your own
public IP**. Anything else means the hop count is wrong:

- `203.0.113.9`: one hop too many; the app is trusting what the client sent.
- A Cloudflare address (such as `172.64.0.0/13` or `104.16.0.0/13`): one hop too few. The
  app doesn't warn about this one, because the address is public.
- A private address, with the warning `client IP from X-Forwarded-For is not a public
  address; check -proxy-hops`: one hop too few, and the extra hop is inside Render.

To find the right count, change `RSS_PROXY_HOPS` on the service's **Environment** page
(saving redeploys) and repeat the check, then set the value that works in `main.tf` and
apply. If no value works, set it to `0` and stop: every visitor then shares one login
limit, but nobody can forge their address.

## Facts recorded after setup

Filled in by #214, checked on 2026-10-13.

- **Log retention:** 7 days on the Hobby workspace ([logging docs](https://render.com/docs/logging),
  [pricing](https://render.com/pricing#plans)). That bounds how far back an incident can be
  investigated, and how long the client IPs in the request logs are kept. Logs past it
  are gone even after upgrading; keeping them longer needs a log stream.
- **Cost:** Render's projection for October 2026 was $10.67 for the web service and
  database, created on 13 October, so a partial month.
