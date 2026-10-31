#!/usr/bin/env bash
# Puts a CloudFront distribution with origin access control (OAC) in front of
# the spike-sse-lambda Function URL, and lets only that distribution invoke
# the function. Safe to re-run. Needs ../sse-lambda/deploy.sh first.
# Cost: $0 within CloudFront's always-free tier.
#
# The *.cloudfront.net address is public while it exists: run ./teardown.sh
# when done.
set -euo pipefail
cd "$(dirname "$0")"
export AWS_PROFILE=${AWS_PROFILE:-cferrante-dev-admin}
export AWS_REGION=${AWS_REGION:-us-east-1}
fn=spike-sse-lambda
oac_name=spike-sse-lambda-oac
comment=spike-cloudfront-sse
# AWS managed policies: no caching at all, and forward everything from the
# viewer except Host (a Function URL rejects a Host header that isn't its own).
caching_disabled=4135ea2d-6df8-44a3-9df3-4b5a84be39ad
all_viewer_except_host=b689b0a8-53d0-40ab-baf2-68738e2966ac

aws sts get-caller-identity >/dev/null 2>&1 \
  || { echo "no valid session; run: aws sso login --profile $AWS_PROFILE" >&2; exit 1; }
lambda_url=$(aws lambda get-function-url-config --function-name "$fn" --query FunctionUrl --output text 2>/dev/null) \
  || { echo "no Function URL for $fn; run ../sse-lambda/deploy.sh first" >&2; exit 1; }
origin=${lambda_url#https://}
origin=${origin%/}

echo "== origin access control $oac_name"
oac=$(aws cloudfront list-origin-access-controls \
  --query "OriginAccessControlList.Items[?Name=='$oac_name'].Id | [0]" --output text)
if [[ $oac == None ]]; then
  # "always" signs every request to the origin with SigV4.
  oac=$(aws cloudfront create-origin-access-control --origin-access-control-config \
    "Name=$oac_name,SigningProtocol=sigv4,SigningBehavior=always,OriginAccessControlOriginType=lambda" \
    --query OriginAccessControl.Id --output text)
  echo "   created"
else
  echo "   exists"
fi

echo "== distribution ($comment)"
dist=$(aws cloudfront list-distributions \
  --query "DistributionList.Items[?Comment=='$comment'].Id | [0]" --output text)
if [[ $dist == None ]]; then
  config=$(cat <<JSON
{
  "CallerReference": "$comment-$(date +%s)",
  "Comment": "$comment",
  "Enabled": true,
  "PriceClass": "PriceClass_100",
  "HttpVersion": "http2",
  "Origins": {"Quantity": 1, "Items": [{
    "Id": "function-url",
    "DomainName": "$origin",
    "OriginAccessControlId": "$oac",
    "CustomOriginConfig": {
      "HTTPPort": 80, "HTTPSPort": 443, "OriginProtocolPolicy": "https-only",
      "OriginSslProtocols": {"Quantity": 1, "Items": ["TLSv1.2"]},
      "OriginReadTimeout": 30, "OriginKeepaliveTimeout": 5
    }
  }]},
  "DefaultCacheBehavior": {
    "TargetOriginId": "function-url",
    "ViewerProtocolPolicy": "https-only",
    "AllowedMethods": {"Quantity": 7,
      "Items": ["GET", "HEAD", "OPTIONS", "PUT", "POST", "PATCH", "DELETE"],
      "CachedMethods": {"Quantity": 2, "Items": ["GET", "HEAD"]}},
    "CachePolicyId": "$caching_disabled",
    "OriginRequestPolicyId": "$all_viewer_except_host",
    "Compress": false
  }
}
JSON
)
  dist=$(aws cloudfront create-distribution --distribution-config "$config" \
    --query Distribution.Id --output text)
  echo "   created"
else
  echo "   exists"
fi
read -r dist_arn domain < <(aws cloudfront get-distribution --id "$dist" \
  --query 'Distribution.[ARN,DomainName]' --output text)

echo "== permission: only this distribution may invoke $fn"
# AWS's OAC-for-Lambda setup grants both actions, each limited to this
# distribution's ARN.
for action in lambda:InvokeFunctionUrl lambda:InvokeFunction; do
  sid=cloudfront-oac-${action#lambda:}
  if aws lambda get-policy --function-name "$fn" --query Policy --output text 2>/dev/null | grep -q "\"$sid\""; then
    echo "   $action exists"
  else
    aws lambda add-permission --function-name "$fn" --statement-id "$sid" --action "$action" \
      --principal cloudfront.amazonaws.com --source-arn "$dist_arn" >/dev/null
    echo "   $action granted"
  fi
done

echo "== waiting for the distribution to deploy (often 3-10 minutes)"
aws cloudfront wait distribution-deployed --id "$dist"
echo "   deployed: https://$domain/"
echo "Next: ./check.sh"
