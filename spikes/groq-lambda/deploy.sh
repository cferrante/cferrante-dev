#!/usr/bin/env bash
# Deploys the Groq spike Lambda. No Function URL: bench.sh invokes it
# directly, so nothing is reachable from outside the account. Its role may
# read only the Groq key parameter. Safe to re-run.
# Cost: $0 within the Lambda free tier; Groq calls use the free tier.
#
#   ./deploy.sh
#
# Needs the key stored first (infra/bootstrap/aws/06-groq-key.sh). Uses
# AWS_PROFILE (default cferrante-dev-admin). Remove with ./teardown.sh.
set -euo pipefail
cd "$(dirname "$0")"

export AWS_PROFILE=${AWS_PROFILE:-cferrante-dev-admin}
export AWS_REGION=${AWS_REGION:-us-east-1}
fn=spike-groq-lambda
role=spike-groq-lambda-role
param=${GROQ_KEY_PARAM:-/cferrante-dev/groq-api-key}
model=${GROQ_MODEL:-openai/gpt-oss-120b}

aws sts get-caller-identity >/dev/null 2>&1 \
  || { echo "no valid session; run: aws sso login --profile $AWS_PROFILE" >&2; exit 1; }
type=$(aws ssm get-parameter --name "$param" --query Parameter.Type --output text 2>/dev/null) \
  || { echo "no parameter $param; run infra/bootstrap/aws/06-groq-key.sh first" >&2; exit 1; }
[[ $type == SecureString ]] || { echo "$param is a $type, want SecureString" >&2; exit 1; }
account=$(aws sts get-caller-identity --query Account --output text)

echo "== build"
mkdir -p build
# Static (no cgo); lambda.norpc drops the legacy RPC runtime; -s -w strips
# debug symbols, roughly a third of the size with the AWS SDK linked in.
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -tags lambda.norpc -ldflags='-s -w' \
  -o build/bootstrap ./cmd/bootstrap
rm -f build/function.zip
zip -q -j build/function.zip build/bootstrap
echo "   $(du -h build/bootstrap | cut -f1) binary"

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
# Least privilege: this one parameter. Decryption with the AWS-managed
# aws/ssm key is allowed by that key's own policy for calls through SSM.
aws iam put-role-policy --role-name "$role" --policy-name read-groq-key --policy-document \
  "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":\"ssm:GetParameter\",\"Resource\":\"arn:aws:ssm:$AWS_REGION:$account:parameter$param\"}]}"
echo "   may read $param only"

echo "== function $fn"
env_vars="Variables={GROQ_KEY_PARAM=$param,GROQ_MODEL=$model}"
if aws lambda get-function --function-name "$fn" >/dev/null 2>&1; then
  aws lambda update-function-code --function-name "$fn" \
    --zip-file fileb://build/function.zip >/dev/null
  aws lambda wait function-updated-v2 --function-name "$fn"
  aws lambda update-function-configuration --function-name "$fn" \
    --memory-size 128 --timeout 30 --environment "$env_vars" >/dev/null
  echo "   code and configuration updated"
else
  # A new role takes a few seconds before Lambda may use it; retry until then.
  for i in $(seq 10); do
    if aws lambda create-function --function-name "$fn" --runtime provided.al2023 \
        --architectures x86_64 --handler bootstrap --role "$role_arn" \
        --memory-size 128 --timeout 30 --environment "$env_vars" \
        --zip-file fileb://build/function.zip >/dev/null 2>build/create.err; then
      echo "   created"; break
    fi
    grep -q 'cannot be assumed' build/create.err || { cat build/create.err >&2; exit 1; }
    (( i < 10 )) || { echo "role still not usable; re-run later" >&2; exit 1; }
    sleep 5
  done
fi
aws lambda wait function-updated-v2 --function-name "$fn"
echo "   ready: 128 MB, x86_64, model $model"
echo "Next: ./bench.sh"
