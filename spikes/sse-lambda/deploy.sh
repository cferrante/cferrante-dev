#!/usr/bin/env bash
# Deploys the SSE spike to Lambda behind a Function URL that requires AWS
# signatures (AWS_IAM), so it is never publicly callable. Safe to re-run.
# Cost: $0 within the Lambda free tier.
#
#   ./deploy.sh [stream|buffered]    (default: stream)
#
# "buffered" sets the URL's invoke mode to BUFFERED, which should make
# watch.sh fail; "stream" sets RESPONSE_STREAM, which should make it pass.
# Uses AWS_PROFILE (default cferrante-dev-admin). Remove with ./teardown.sh.
set -euo pipefail
cd "$(dirname "$0")"

case ${1:-stream} in
  stream) mode=RESPONSE_STREAM ;;
  buffered) mode=BUFFERED ;;
  *) echo "usage: $0 [stream|buffered]" >&2; exit 2 ;;
esac
export AWS_PROFILE=${AWS_PROFILE:-cferrante-dev-admin}
export AWS_REGION=${AWS_REGION:-us-east-1}
fn=spike-sse-lambda
role=spike-sse-lambda-role

aws sts get-caller-identity >/dev/null 2>&1 \
  || { echo "no valid session; run: aws sso login --profile $AWS_PROFILE" >&2; exit 1; }

echo "== build"
mkdir -p build
# No cgo here, so the binary is static by default. lambda.norpc drops the
# legacy Go RPC runtime that provided.al2023 doesn't use.
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -tags lambda.norpc \
  -o build/bootstrap ./cmd/bootstrap
zip -q -j build/function.zip build/bootstrap

echo "== role $role"
if ! role_arn=$(aws iam get-role --role-name "$role" --query Role.Arn --output text 2>/dev/null); then
  role_arn=$(aws iam create-role --role-name "$role" --query Role.Arn --output text \
    --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}')
  echo "   created"
else
  echo "   exists"
fi
aws iam attach-role-policy --role-name "$role" \
  --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole

echo "== function $fn"
if aws lambda get-function --function-name "$fn" >/dev/null 2>&1; then
  aws lambda update-function-code --function-name "$fn" \
    --zip-file fileb://build/function.zip >/dev/null
  echo "   code updated"
else
  # A new role takes a few seconds before Lambda may use it; retry until then.
  for i in $(seq 10); do
    if aws lambda create-function --function-name "$fn" --runtime provided.al2023 \
        --architectures x86_64 --handler bootstrap --role "$role_arn" \
        --memory-size 128 --timeout 15 --zip-file fileb://build/function.zip \
        >/dev/null 2>build/create.err; then
      echo "   created"; break
    fi
    grep -q 'cannot be assumed' build/create.err || { cat build/create.err >&2; exit 1; }
    (( i < 10 )) || { echo "role still not usable; re-run later" >&2; exit 1; }
    sleep 5
  done
fi
aws lambda wait function-updated-v2 --function-name "$fn"

echo "== function URL ($mode, AWS_IAM auth)"
if aws lambda get-function-url-config --function-name "$fn" >/dev/null 2>&1; then
  aws lambda update-function-url-config --function-name "$fn" \
    --auth-type AWS_IAM --invoke-mode "$mode" >/dev/null
  echo "   updated"
else
  aws lambda create-function-url-config --function-name "$fn" \
    --auth-type AWS_IAM --invoke-mode "$mode" >/dev/null
  echo "   created"
fi
echo "   $(aws lambda get-function-url-config --function-name "$fn" --query FunctionUrl --output text)"
echo "Next: ./watch.sh"
