#!/usr/bin/env bash
# Creates the public DNS zone for $DOMAIN in the project account and points the
# domain's registration (which stays in the management account) at it.
# Cost: $0.50/month for the zone. Optional until plan step 7.2.
source "$(dirname "$0")/lib.sh"
require DOMAIN
ensure_login "$MGMT_PROFILE"
ensure_login "$PROJECT_PROFILE"

zone=$(zone_id project)
if [[ $zone == None ]]; then
  act "creating hosted zone for $DOMAIN in the project account"
  # The caller reference only has to be unique per request; the lookup above
  # is what keeps re-runs from creating a second zone.
  zone=$(project route53 create-hosted-zone --name "$DOMAIN" \
    --caller-reference "$DOMAIN-$(date +%s)" --hosted-zone-config Comment="cferrante-dev site" \
    --query HostedZone.Id --output text | sed 's#^/hostedzone/##')
  ok "zone created"
else
  ok "zone for $DOMAIN exists in the project account"
fi

want=$(zone_ns "$zone")
if [[ $want == "$(domain_ns)" ]]; then
  ok "$DOMAIN already uses the project account's name servers"
else
  act "pointing $DOMAIN at the project account's name servers"
  args=()
  while read -r ns; do args+=("Name=$ns"); done <<<"$want"
  op=$(mgmt route53domains update-domain-nameservers --region us-east-1 --domain-name "$DOMAIN" \
    --nameservers "${args[@]}" --query OperationId --output text)
  ok "requested (operation $op). Usually live within minutes; check with:  dig NS $DOMAIN +short"
fi

old=$(zone_id mgmt)
if [[ $old != None ]]; then
  echo "  note: the management account still has a zone for $DOMAIN ($old), created when you"
  echo "        registered the domain. It is no longer used and costs \$0.50/month. Check that it"
  echo "        holds only NS and SOA records, then delete it in the Route 53 console."
fi
