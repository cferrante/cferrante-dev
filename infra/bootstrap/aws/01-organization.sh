#!/usr/bin/env bash
# Makes the current account the management account of an AWS Organization
# with all features (needed for Identity Center access to member accounts).
# Cost: $0.
source "$(dirname "$0")/lib.sh"
ensure_login "$MGMT_PROFILE"

if read -r master features < <(mgmt organizations describe-organization \
    --query 'Organization.[MasterAccountId,FeatureSet]' --output text 2>/dev/null); then
  me=$(mgmt sts get-caller-identity --query Account --output text)
  [[ $master == "$me" ]] || die "this account is a member of another organization; run from its management account"
  [[ $features == ALL ]] || die "organization has feature set $features; enable all features in the Organizations console"
  ok "organization exists with all features"
else
  act "creating organization (all features)"
  mgmt organizations create-organization --feature-set ALL >/dev/null
  ok "organization created. AWS emails the account owner a verification link; click it"
fi
