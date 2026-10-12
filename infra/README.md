# infra

Terraform for production on Render: one Postgres 17 database (`rss-db`) and one web
service (`rss`) running `ghcr.io/grodier/rss`, both in Virginia. State lives in the
Cloudflare R2 bucket `rss-tfstate`. Why Render and this setup: "Deployment" in
[`docs/decisions.md`](../docs/decisions.md).

Terraform doesn't own two things that change outside it, and ignores them:

- **The image.** Deploys set it by digest; `apply` never rolls a deploy back.
- **`RSS_ALLOW_RESET_TO`**, set and cleared by the "Reset production database" workflow.

## Prerequisites

The accounts, buckets, tokens and GitHub settings from #208, and Terraform 1.10 or later.

## Running Terraform locally

Take every value from your password manager, never from a file in the repository:

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
terraform -chdir=infra apply
```

Read the plan before applying: nothing should be destroyed or replaced unless you meant
it, and no secret should appear in plain text. The database has `prevent_destroy`.

`terraform -chdir=infra output -raw url` prints the service's URL, and `output -raw
service_id` its ID.

## First account

Signup is off. To create an account, turn it on briefly:

```sh
terraform -chdir=infra apply -var signup=true
# sign up at <url>/signup
terraform -chdir=infra apply    # signup off again; /signup returns 404
```

The shared invite code (Pre-alpha testing in [`docs/todo.md`](../docs/todo.md)) later
replaces this.

## Measuring Render's proxy range

`trusted_proxies` in `terraform.tfvars` starts empty, so the app trusts no proxy and
logs Render's proxy address as every client's IP. To measure it:

1. With `trusted_proxies = ""`, load a few pages. In Render's logs, the JSON request
   log's `ip` is a Render proxy address, not yours.
2. Set `trusted_proxies` in `terraform.tfvars` to the narrowest range covering those
   addresses, or the range Render documents if it does. Apply.
3. Run `curl -H 'X-Forwarded-For: 203.0.113.9' "$URL/login"`. The log's `ip` must be
   **your own public IP**: not `203.0.113.9`, and not a proxy address.

If it isn't, or the logs show more than one proxy hop, set `trusted_proxies` back to
`""`, apply, and stop: until it's fixed, every visitor shares one login limit.

## Facts recorded after setup

Filled in by #214.

- Render's log retention on the Hobby workspace: _not recorded yet_.
- Render's proxy range: _not measured yet_ (measured on: _date_).
