#!/usr/bin/env bash
# Creates the project's member account in the organization. Cost: $0; its
# usage is billed to the management account (consolidated billing).
source "$(dirname "$0")/lib.sh"
require PROJECT_ACCOUNT_NAME PROJECT_ACCOUNT_EMAIL
ensure_login "$MGMT_PROFILE"

if [[ $(project_account_id) != None ]]; then
  ok "account $PROJECT_ACCOUNT_NAME exists"
  exit 0
fi

# Resume a creation an interrupted earlier run already started.
req=$(mgmt organizations list-create-account-status --states IN_PROGRESS \
  --query "CreateAccountStatuses[?AccountName=='$PROJECT_ACCOUNT_NAME'].Id | [0]" --output text)
if [[ $req == None ]]; then
  act "creating account $PROJECT_ACCOUNT_NAME"
  # ALLOW lets IAM/SSO roles in the account use billing features (budgets).
  req=$(mgmt organizations create-account --email "$PROJECT_ACCOUNT_EMAIL" \
    --account-name "$PROJECT_ACCOUNT_NAME" --iam-user-access-to-billing ALLOW \
    --query CreateAccountStatus.Id --output text)
else
  act "account creation already in progress"
fi

for _ in $(seq 60); do
  read -r state reason < <(mgmt organizations describe-create-account-status \
    --create-account-request-id "$req" --query 'CreateAccountStatus.[State,FailureReason]' --output text)
  case $state in
    SUCCEEDED) ok "account $PROJECT_ACCOUNT_NAME created"; exit 0 ;;
    FAILED)    die "account creation failed: $reason" ;;
  esac
  sleep 10
done
die "still in progress after 10 minutes; re-run this script later"
