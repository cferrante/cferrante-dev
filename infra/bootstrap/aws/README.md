# AWS bootstrap

Shell scripts that set up the AWS account this project deploys into. They run once, by hand,
before anything else in [`infra/`](..) can work. See [design §13](../../../docs/design.md#13-aws-accounts-and-dns)
for why the accounts are laid out this way.

**Scripts vs OpenTofu.** OpenTofu (plan [Phase 7](../../../docs/plan.md#phase-7--infrastructure-and-private-deploy-m4))
manages everything *inside* the project account, but it needs that account, a sign-in and a place
to keep its state before it can run. These scripts create those prerequisites. Account-level
resources also change rarely and are risky to delete by accident, so they are kept out of the
state OpenTofu can destroy.

## What you end up with

```
Management account (existing)            Project account "cferrante-dev" (new)
├─ AWS Organization                     ├─ Your Identity Center user: AdministratorAccess
├─ IAM Identity Center (your sign-in)   ├─ Budget: $1/month, 3 email alerts
└─ Domain registration: cferrante.dev ──┼─▶ DNS zone: cferrante.dev
                                        └─ Everything OpenTofu builds later
```

## Assumptions

- **AWS CLI v2** and **bash**. No other tools (no `jq`).
- An existing AWS account with **IAM Identity Center** enabled (organization instance) and a
  user you can sign in with, assigned an admin permission set.
- A CLI profile for that account, created with `aws configure sso`.
- The domain is **registered with Route 53 in that account**.
- You are allowed to create AWS accounts (new organizations allow a small number at first).

## Setup

```bash
cd infra/bootstrap/aws
cp config.env.example config.env     # git-ignored; fill in the blanks
aws sso login --profile <MGMT_PROFILE>
./check.sh                           # read-only: shows ✓ / ✗ for each step
```

## Steps

Run them in order. Each one checks what already exists and only creates what's missing, so
it is safe to re-run: after an interruption, after a failure, or just to confirm. Re-running a
finished step prints ✓ "exists" and changes nothing.

| Script | Does | Account | Cost |
|---|---|---|---|
| `check.sh` | Reports which steps are done. Changes nothing | both | $0 |
| `01-organization.sh` | Makes the existing account the management account of an organization with all features | management | $0 |
| `02-account.sh` | Creates the project account (waits up to 10 min) | management | $0 |
| `03-sso-access.sh` | Grants your user the permission set in the project account; writes the `PROJECT_PROFILE` CLI profile | management | $0 |
| `04-budget.sh` | Monthly cost budget with alerts at 1% and 100% actual, 100% forecast | project | $0 (budgets without actions are free) |
| `05-dns.sh` | Creates the domain's DNS zone in the project account and points the registration at it | both | $0.50/month |

After `03-sso-access.sh`, sign in to the new profile once:

```bash
aws sso login --profile <PROJECT_PROFILE>
```

Use `./check.sh` as the test for each step: ✗ before, ✓ after.

## Costs

- Organizations, Identity Center and budgets without actions are free.
- The DNS zone is $0.50/month. Registering a domain with Route 53 also creates a zone in the
  management account; once `05-dns.sh` has moved DNS, that zone is unused and should be deleted
  (see below), or you pay for both.
- Charges from the project account appear on the management account's bill (consolidated
  billing). Free-tier allowances are shared across the organization, not given per account.

## After 05-dns.sh: delete the old zone

DNS answers are cached for as long as their time-to-live (TTL); Route 53 name-server records
default to two days. Your resolver may keep showing the old name servers for hours after the
change. To check the change itself, ask the `.dev` registry directly:

```bash
dig NS cferrante.dev @"$(dig NS dev. +short | head -1)" +norec +noall +authority
```

When plain `dig NS cferrante.dev +short` also shows the new zone's name servers, open Route 53
in the **management** account, check its `cferrante.dev` zone holds only NS and SOA records,
and delete it.

## Troubleshooting

| Message | Fix |
|---|---|
| `no valid session for profile …` | `aws sso login --profile …` (sessions expire after a few hours) |
| `Missing …/config.env` | `cp config.env.example config.env` and fill it in |
| `No access` when signing in to the project profile | The new assignment takes a minute to apply; retry |
| `account creation failed: EMAIL_ALREADY_EXISTS` | `PROJECT_ACCOUNT_EMAIL` belongs to another AWS account; use a different (plus) address |
| `still in progress …; re-run this script later` | Re-run it: the script resumes rather than starting again |

## Undo

Nothing here deletes resources; do it in the console, in reverse order:

1. **DNS:** point the domain back at a zone in the management account (Route 53 → Registered
   domains → Edit name servers), then delete the project account's zone.
2. **Budget:** Billing → Budgets in the project account.
3. **Access:** IAM Identity Center → AWS accounts → remove the assignment; delete the profile
   from `~/.aws/config`.
4. **Account:** Organizations → close the account. Closed accounts can be reopened for 90 days.
