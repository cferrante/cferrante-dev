---
title: "Staff Software Engineer & Technical Partner, Legal Tech Consultancy (Aug 2016 – present)"
url: /experience/legal-tech-consultancy
access: public
---
## System ownership

Since August 2016 Colleen has been self-employed as a staff software engineer and technical
partner to a legal recruiting business. She owns its CRM's architecture, security, roadmap and
operations as its only engineer, serving 3 partners and about 50 attorney users. The entire
business runs through it: 142,000+ candidate records, 5,800+ client firms, 22,400+ searches and
460+ completed placements.

## Restarting a broken CRM

Colleen inherited the CRM abandoned and non-functional after six years of stalled development.
She shadowed users to find the workflows the business couldn't run without, then repaired only
those code paths, leaving the stack, database and infrastructure unchanged to keep risk low. All
partners and users were back in daily use of those workflows within one week, without a rewrite.

## Release cadence

After six stalled years the partners were anxious and the users impatient. Colleen set a cadence
of same-day to one-week releases, each scoped from how users actually work and checked with the
person who asked for it. Bug reports fell sharply, and the partners then trusted her with their
most valuable asset: cleaning up the candidate data.

## Candidate deduplication

Search results couldn't be trusted because candidates appeared multiple times. Colleen defined a
duplicate as a match on any of 5 combinations of 13 identifying fields, then built stored
procedures and review views so users confirmed merges while automatic merging was built up step
by step. The initial cleanup removed 43,800+ duplicates, nearly a quarter of all records. New
duplicates are stopped at import by uniqueness checks plus user review of borderline matches,
and Twilio SendGrid bounce reports flag dead work emails on a candidate's record, a sign the
candidate has changed employers.

## Security

Colleen spotted suspicious traffic in the logs and assessed the risk before it became an
incident: a leak of 142,000+ attorneys' confidential job searches would expose candidates to
their employers and damage client trust. She layered AWS WAF, an Application Load Balancer and
isolated security groups so only filtered traffic reaches the application, keeping bots and
attacks out. Over the last 15 months the WAF blocked about 87,000 requests, 12% of all traffic.

## Microsoft 365 sign-in

The CRM's homegrown login had no multi-factor authentication. Colleen explained the risk and
cost tradeoffs to the non-technical partners in business terms, and they chose Microsoft 365
sign-in over OAuth2 with MFA enforced. For the optional email-and-password login that remains,
she added WAF IP allow and block lists to limit who can reach it.

## Email archiving

The partners found the Outlook add-ins they had evaluated too heavy, so Colleen proposed a
backend service instead. It uses each user's OAuth2 token with the Microsoft Graph API to archive
emails and attachments as static HTML in S3, and links each one to the matching candidate or
employer contact by email address, with no extra steps for users. More than 98,000 emails have
been archived to date.

## Performance

Colleen measured page loads taking seconds in Chrome DevTools, then worked down the stack one
layer at a time. She migrated the flat database to a relational design over several phases,
refactored copy-pasted code into a model-view-controller structure as each part of the app was
touched, and moved the front end to S3 and CloudFront. Page loads fell from seconds to under
650 ms.
