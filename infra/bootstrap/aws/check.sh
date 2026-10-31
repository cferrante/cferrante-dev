#!/usr/bin/env bash
# Read-only: reports which bootstrap steps are done. Changes nothing.
# Run it before and after each step: ✗ turning into ✓ is the "test".
source "$(dirname "$0")/lib.sh"

echo "Management account ($MGMT_PROFILE)"
ensure_login "$MGMT_PROFILE"
if master=$(mgmt organizations describe-organization --query Organization.MasterAccountId --output text 2>/dev/null); then
  me=$(mgmt sts get-caller-identity --query Account --output text)
  if [[ $master == "$me" ]]; then ok "01 organization exists; this is its management account"
  else todo "01 this account belongs to another organization"; fi
else
  todo "01 no organization"
fi

account_id=$(project_account_id 2>/dev/null || echo None)
if [[ $account_id != None ]]; then ok "02 account $PROJECT_ACCOUNT_NAME exists"
else todo "02 account $PROJECT_ACCOUNT_NAME"; fi

if [[ $account_id != None && -n ${SSO_USERNAME:-} ]]; then
  read -r instance_arn store_id <<<"$(sso_instance)"
  ps_arn=$(permission_set_arn "$instance_arn")
  user_id=$(sso_user_id "$store_id")
  n=$(mgmt sso-admin list-account-assignments --instance-arn "$instance_arn" --account-id "$account_id" \
    --permission-set-arn "$ps_arn" --query "length(AccountAssignments[?PrincipalId=='$user_id'])" --output text)
  if (( n > 0 )); then ok "03 $SSO_USERNAME has $PERMISSION_SET_NAME in the project account"
  else todo "03 $SSO_USERNAME access to the project account"; fi
else
  todo "03 SSO access (needs step 02 and SSO_USERNAME)"
fi

echo "Project account ($PROJECT_PROFILE)"
if ! aws --profile "$PROJECT_PROFILE" sts get-caller-identity >/dev/null 2>&1; then
  todo "03 profile $PROJECT_PROFILE missing or not logged in (aws sso login --profile $PROJECT_PROFILE)"
  todo "04 budget (needs the profile)"
  todo "05 DNS (needs the profile)"
  todo "06 Groq API key (needs the profile)"
  exit 0
fi
ok "03 profile $PROJECT_PROFILE works"

acct=$(project sts get-caller-identity --query Account --output text)
if n=$(project budgets describe-notifications-for-budget --account-id "$acct" --budget-name "$BUDGET_NAME" \
    --query 'length(Notifications)' --output text 2>/dev/null); then
  if (( n >= 3 )); then ok "04 budget $BUDGET_NAME with $n alerts"
  else todo "04 budget $BUDGET_NAME has $n of 3 alerts"; fi
else
  todo "04 budget $BUDGET_NAME"
fi

zone=$(zone_id project)
if [[ $zone == None ]]; then
  todo "05 hosted zone for $DOMAIN in the project account"
elif [[ "$(zone_ns "$zone")" == "$(domain_ns)" ]]; then
  ok "05 $DOMAIN is delegated to the project account's zone"
else
  todo "05 $DOMAIN's name servers don't point at the project account's zone yet"
fi

# Metadata only: the value is never read here.
if type=$(project ssm describe-parameters \
    --parameter-filters "Key=Name,Values=$GROQ_KEY_PARAM" \
    --query 'Parameters[0].Type' --output text 2>/dev/null) && [[ $type == SecureString ]]; then
  ok "06 Groq API key stored encrypted at $GROQ_KEY_PARAM"
else
  todo "06 Groq API key at $GROQ_KEY_PARAM"
fi
