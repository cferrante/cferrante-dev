# shellcheck shell=bash
# Shared helpers for the bootstrap scripts. Sourced by each script, not run.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ ! -f "$here/config.env" ]]; then
  echo "Missing $here/config.env. Copy config.env.example to config.env and fill it in." >&2
  exit 1
fi
# shellcheck source=config.env.example
source "$here/config.env"

# Defaults for settings added after config.env was first written.
GROQ_KEY_PARAM=${GROQ_KEY_PARAM:-/cferrante-dev/groq-api-key}

# shellcheck source=secret.sh
source "$here/secret.sh"

ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; }
todo() { printf '  \033[33m✗\033[0m %s\n' "$*"; }
act()  { printf '  \033[36m→\033[0m %s\n' "$*"; }
die()  { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }

# require VAR... : stop unless each config value is set.
require() {
  local v
  for v in "$@"; do [[ -n "${!v:-}" ]] || die "set $v in $here/config.env"; done
}

mgmt()    { aws --profile "$MGMT_PROFILE" "$@"; }
project() { aws --profile "$PROJECT_PROFILE" "$@"; }

# ensure_login PROFILE : stop with the fix if the SSO token is missing or expired.
ensure_login() {
  aws --profile "$1" sts get-caller-identity >/dev/null 2>&1 \
    || die "no valid session for profile $1. Run:  aws sso login --profile $1"
}

# Prints the project account's ID, or "None" if it doesn't exist yet.
project_account_id() {
  mgmt organizations list-accounts \
    --query "Accounts[?Name=='$PROJECT_ACCOUNT_NAME' && Status=='ACTIVE'].Id | [0]" --output text
}

# Prints "INSTANCE_ARN IDENTITY_STORE_ID" of the IAM Identity Center instance.
sso_instance() {
  mgmt sso-admin list-instances --query 'Instances[0].[InstanceArn,IdentityStoreId]' --output text
}

# permission_set_arn INSTANCE_ARN : ARN of $PERMISSION_SET_NAME, or empty.
permission_set_arn() {
  local arn name
  for arn in $(mgmt sso-admin list-permission-sets --instance-arn "$1" --query PermissionSets --output text); do
    name=$(mgmt sso-admin describe-permission-set --instance-arn "$1" --permission-set-arn "$arn" \
      --query PermissionSet.Name --output text)
    if [[ $name == "$PERMISSION_SET_NAME" ]]; then echo "$arn"; return; fi
  done
}

# sso_user_id STORE_ID : Identity Center user ID for $SSO_USERNAME.
sso_user_id() {
  mgmt identitystore get-user-id --identity-store-id "$1" \
    --alternate-identifier "{\"UniqueAttribute\":{\"AttributePath\":\"userName\",\"AttributeValue\":\"$SSO_USERNAME\"}}" \
    --query UserId --output text
}

# Prints the ID (without the /hostedzone/ prefix) of the public zone for
# $DOMAIN in the account behind CLI function $1 (mgmt or project), or "None".
zone_id() {
  "$1" route53 list-hosted-zones-by-name --dns-name "$DOMAIN." \
    --query "HostedZones[?Name=='$DOMAIN.' && Config.PrivateZone==\`false\`].Id | [0]" --output text \
    | sed 's#^/hostedzone/##'
}

# Sorted, lowercase name servers: of a zone in the project account, and of the
# domain registration in the management account.
zone_ns()   { project route53 get-hosted-zone --id "$1" --query DelegationSet.NameServers --output text | tr '\t' '\n' | tr '[:upper:]' '[:lower:]' | sort; }
domain_ns() { mgmt route53domains get-domain-detail --region us-east-1 --domain-name "$DOMAIN" --query 'Nameservers[].Name' --output text | tr '\t' '\n' | tr '[:upper:]' '[:lower:]' | sed 's/\.$//' | sort; }
