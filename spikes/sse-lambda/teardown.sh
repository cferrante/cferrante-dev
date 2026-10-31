#!/usr/bin/env bash
# Removes everything deploy.sh created. Safe to re-run.
set -euo pipefail
export AWS_PROFILE=${AWS_PROFILE:-cferrante-dev-admin}
export AWS_REGION=${AWS_REGION:-us-east-1}
fn=spike-sse-lambda
role=spike-sse-lambda-role

aws lambda delete-function --function-name "$fn" >/dev/null 2>&1 && echo "function and URL deleted" || echo "function already gone"
aws logs delete-log-group --log-group-name "/aws/lambda/$fn" 2>/dev/null && echo "logs deleted" || echo "logs already gone"
aws iam detach-role-policy --role-name "$role" \
  --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole 2>/dev/null || true
aws iam delete-role --role-name "$role" 2>/dev/null && echo "role deleted" || echo "role already gone"
