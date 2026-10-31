#!/usr/bin/env bash
# Removes what deploy.sh created: the Lambda permissions, the distribution
# (disabled first, as CloudFront requires) and the origin access control.
# Safe to re-run. Disabling takes several minutes. Leaves the function itself
# to ../sse-lambda/teardown.sh.
set -euo pipefail
export AWS_PROFILE=${AWS_PROFILE:-cferrante-dev-admin}
export AWS_REGION=${AWS_REGION:-us-east-1}
fn=spike-sse-lambda
oac_name=spike-sse-lambda-oac
comment=spike-cloudfront-sse

for sid in cloudfront-oac-InvokeFunctionUrl cloudfront-oac-InvokeFunction; do
  aws lambda remove-permission --function-name "$fn" --statement-id "$sid" >/dev/null 2>&1 \
    && echo "permission $sid removed" || echo "permission $sid already gone"
done

dist=$(aws cloudfront list-distributions \
  --query "DistributionList.Items[?Comment=='$comment'].Id | [0]" --output text)
if [[ $dist != None ]]; then
  read -r etag enabled < <(aws cloudfront get-distribution-config --id "$dist" \
    --query '[ETag,DistributionConfig.Enabled]' --output text)
  if [[ $enabled == True ]]; then
    config=$(aws cloudfront get-distribution-config --id "$dist" --query DistributionConfig --output json \
      | sed '0,/"Enabled": true/s//"Enabled": false/')
    aws cloudfront update-distribution --id "$dist" --if-match "$etag" \
      --distribution-config "$config" >/dev/null
    echo "distribution disabled; waiting for it to deploy (often 3-10 minutes)"
  fi
  aws cloudfront wait distribution-deployed --id "$dist"
  etag=$(aws cloudfront get-distribution --id "$dist" --query ETag --output text)
  aws cloudfront delete-distribution --id "$dist" --if-match "$etag"
  echo "distribution deleted"
else
  echo "distribution already gone"
fi

oac=$(aws cloudfront list-origin-access-controls \
  --query "OriginAccessControlList.Items[?Name=='$oac_name'].Id | [0]" --output text)
if [[ $oac != None ]]; then
  etag=$(aws cloudfront get-origin-access-control --id "$oac" --query ETag --output text)
  aws cloudfront delete-origin-access-control --id "$oac" --if-match "$etag"
  echo "origin access control deleted"
else
  echo "origin access control already gone"
fi
