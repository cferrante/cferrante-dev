#!/usr/bin/env bash
# Creates (or resets) the project account's monthly cost budget and its three
# email alerts. Cost: $0 (the first two budgets per account are free).
source "$(dirname "$0")/lib.sh"
require BUDGET_NAME BUDGET_LIMIT_USD BUDGET_EMAIL
ensure_login "$PROJECT_PROFILE"
acct=$(project sts get-caller-identity --query Account --output text)

budget=$(cat <<EOF
{"BudgetName":"$BUDGET_NAME","BudgetType":"COST","TimeUnit":"MONTHLY",
 "BudgetLimit":{"Amount":"$BUDGET_LIMIT_USD","Unit":"USD"},
 "CostTypes":{"IncludeCredit":false,"IncludeRefund":false}}
EOF
)

if project budgets describe-budget --account-id "$acct" --budget-name "$BUDGET_NAME" >/dev/null 2>&1; then
  project budgets update-budget --account-id "$acct" --new-budget "$budget"
  ok "budget $BUDGET_NAME exists (settings reapplied)"
else
  act "creating budget $BUDGET_NAME (\$$BUDGET_LIMIT_USD/month)"
  project budgets create-budget --account-id "$acct" --budget "$budget"
  ok "budget created"
fi

# alert TYPE PERCENT : an alert that already exists is left alone.
alert() {
  local out
  if out=$(project budgets create-notification --account-id "$acct" --budget-name "$BUDGET_NAME" \
      --notification "NotificationType=$1,ComparisonOperator=GREATER_THAN,Threshold=$2,ThresholdType=PERCENTAGE" \
      --subscribers "SubscriptionType=EMAIL,Address=$BUDGET_EMAIL" 2>&1); then
    ok "alert $1 > $2% created"
  elif grep -q DuplicateRecordException <<<"$out"; then
    ok "alert $1 > $2% exists"
  else
    die "$out"
  fi
}
alert ACTUAL 1        # any spend at all (1 cent)
alert ACTUAL 100      # over budget
alert FORECASTED 100  # on track to go over
