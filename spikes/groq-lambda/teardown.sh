#!/usr/bin/env bash
# Removes everything deploy.sh created. Safe to re-run. The Groq key is not
# touched: it belongs to infra/bootstrap/aws (06-groq-key.sh), and Phase 5
# reuses it; that README's teardown section says how to remove it.
set -euo pipefail
export AWS_PROFILE=${AWS_PROFILE:-cferrante-dev-admin}
export AWS_REGION=${AWS_REGION:-us-east-1}
fn=spike-groq-lambda
role=spike-groq-lambda-role

if aws lambda delete-function --function-name "$fn" >/dev/null 2>&1; then echo "function deleted"; else echo "function already gone"; fi
if aws logs delete-log-group --log-group-name "/aws/lambda/$fn" 2>/dev/null; then echo "logs deleted"; else echo "logs already gone"; fi
aws iam delete-role-policy --role-name "$role" --policy-name read-groq-key 2>/dev/null || true
aws iam detach-role-policy --role-name "$role" \
  --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole 2>/dev/null || true
if aws iam delete-role --role-name "$role" 2>/dev/null; then echo "role deleted"; else echo "role already gone"; fi
echo "Groq key left in place (owned by infra/bootstrap/aws)"
