#!/usr/bin/env bash
# Deploys one build variant of the spike to Lambda (provided.al2023, x86_64)
# and invokes it. Safe to re-run: it creates what's missing and updates the
# code otherwise. Cost: $0 within the Lambda free tier.
#
#   ./deploy.sh [full-static|static-libs|naive]     (default: full-static)
#
# Uses AWS_PROFILE (default cferrante-dev-admin). Remove with ./teardown.sh.
set -euo pipefail
cd "$(dirname "$0")"

variant=${1:-full-static}
export AWS_PROFILE=${AWS_PROFILE:-cferrante-dev-admin}
export AWS_REGION=${AWS_REGION:-us-east-1}
fn=spike-cgo-lambda
role=spike-cgo-lambda-role

aws sts get-caller-identity >/dev/null 2>&1 \
  || { echo "no valid session; run: aws sso login --profile $AWS_PROFILE" >&2; exit 1; }

echo "== build $variant"
make -s "$variant"
./check-glibc.sh "build/$variant/bootstrap" || echo "   (deploying anyway to see what Lambda does)"
zip -q -j "build/$variant/function.zip" "build/$variant/bootstrap"

echo "== role $role"
if ! role_arn=$(aws iam get-role --role-name "$role" --query Role.Arn --output text 2>/dev/null); then
  role_arn=$(aws iam create-role --role-name "$role" --query Role.Arn --output text \
    --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}')
  echo "   created"
else
  echo "   exists"
fi
# Only permission: write its own logs. Attaching twice is a no-op.
aws iam attach-role-policy --role-name "$role" \
  --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole

echo "== function $fn"
if aws lambda get-function --function-name "$fn" >/dev/null 2>&1; then
  aws lambda update-function-code --function-name "$fn" \
    --zip-file "fileb://build/$variant/function.zip" --query LastModified --output text >/dev/null
  echo "   code updated"
else
  # A new role takes a few seconds before Lambda may use it; retry until then.
  for i in $(seq 10); do
    if aws lambda create-function --function-name "$fn" --runtime provided.al2023 \
        --architectures x86_64 --handler bootstrap --role "$role_arn" \
        --memory-size 128 --timeout 10 --zip-file "fileb://build/$variant/function.zip" \
        >/dev/null 2>"build/create.err"; then
      echo "   created"; break
    fi
    grep -q 'cannot be assumed' build/create.err || { cat build/create.err >&2; exit 1; }
    (( i < 10 )) || { echo "role still not usable; re-run later" >&2; exit 1; }
    sleep 5
  done
fi
aws lambda wait function-updated-v2 --function-name "$fn"

echo "== invoke"
status=$(aws lambda invoke --function-name "$fn" --cli-binary-format raw-in-base64-out \
  --payload '{"numbers":[1,2,3,4]}' --query '[StatusCode,FunctionError]' --output text \
  build/response.json)
echo "   $status"
echo "   response: $(cat build/response.json)"

# Lambda reports only "exit status 1" for a crash; the cause is in the log.
if [[ $status != *None ]]; then
  echo "== recent log lines"
  sleep 5 # log delivery lags the invoke slightly
  aws logs tail "/aws/lambda/$fn" --since 5m --format short | tail -n 8
fi
