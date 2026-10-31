#!/usr/bin/env bash
# Gives your Identity Center user the admin permission set in the project
# account, and adds a CLI profile for it to ~/.aws/config. Cost: $0.
source "$(dirname "$0")/lib.sh"
require SSO_USERNAME PERMISSION_SET_NAME PROJECT_PROFILE SSO_SESSION
ensure_login "$MGMT_PROFILE"

account_id=$(project_account_id)
[[ $account_id != None ]] || die "project account not found; run 02-account.sh first"

read -r instance_arn store_id <<<"$(sso_instance)"
[[ $instance_arn != None ]] || die "no IAM Identity Center instance found"
ps_arn=$(permission_set_arn "$instance_arn")
[[ -n $ps_arn ]] || die "permission set $PERMISSION_SET_NAME not found"
user_id=$(sso_user_id "$store_id")

n=$(mgmt sso-admin list-account-assignments --instance-arn "$instance_arn" --account-id "$account_id" \
  --permission-set-arn "$ps_arn" --query "length(AccountAssignments[?PrincipalId=='$user_id'])" --output text)
if (( n > 0 )); then
  ok "$SSO_USERNAME already has $PERMISSION_SET_NAME in $PROJECT_ACCOUNT_NAME"
else
  act "assigning $PERMISSION_SET_NAME to $SSO_USERNAME in $PROJECT_ACCOUNT_NAME"
  req=$(mgmt sso-admin create-account-assignment --instance-arn "$instance_arn" \
    --target-id "$account_id" --target-type AWS_ACCOUNT --permission-set-arn "$ps_arn" \
    --principal-type USER --principal-id "$user_id" \
    --query AccountAssignmentCreationStatus.RequestId --output text)
  for _ in $(seq 30); do
    read -r status reason < <(mgmt sso-admin describe-account-assignment-creation-status \
      --instance-arn "$instance_arn" --account-assignment-creation-request-id "$req" \
      --query 'AccountAssignmentCreationStatus.[Status,FailureReason]' --output text)
    case $status in
      SUCCEEDED) ok "assignment created"; break ;;
      FAILED)    die "assignment failed: $reason" ;;
    esac
    sleep 5
  done
  [[ $status == SUCCEEDED ]] || die "assignment still in progress; re-run this script later"
fi

# `configure set` writes the same values every time, so re-running is harmless.
aws configure set sso_session "$SSO_SESSION" --profile "$PROJECT_PROFILE"
aws configure set sso_account_id "$account_id" --profile "$PROJECT_PROFILE"
aws configure set sso_role_name "$PERMISSION_SET_NAME" --profile "$PROJECT_PROFILE"
aws configure set region "$REGION" --profile "$PROJECT_PROFILE"
aws configure set output json --profile "$PROJECT_PROFILE"
ok "profile $PROJECT_PROFILE is in ~/.aws/config"
