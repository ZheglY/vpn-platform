# Owner Inputs Required Before Provider-Specific Work

Provide references or decisions only. Never send passwords, tokens, private
keys, certificates, recovery codes, customer data, or full provider payloads in
chat or Git. Use an approved secret manager, encrypted credential location, OIDC,
or an already authenticated provider CLI session.

1. Seller legal entity, jurisdiction, allowed customer countries, tax/receipt
   obligations, privacy/retention requirements, and approved legal documents.
2. Repository/distribution license decision and counsel decisions for the
   current dependency/license blockers.
3. Monthly budget, approved infrastructure/DNS/VPS/registry/secret/PKI providers,
   regions, data-residency constraints, and provider AUP acceptance.
4. Owned public/private domains and provider account/project identifiers.
5. Primary and secondary alert/escalation contacts plus support/abuse contacts.
6. Approved RPO, RTO, backup/PITR retention, and disaster-recovery authority.
7. References to encrypted credential locations, or confirmation that required
   OIDC/authenticated CLI sessions are configured for the operator.

Until these inputs and real staging evidence exist, provider-specific IaC,
public DNS, registry publication, VPS enrollment, real payments, and production
deployment remain blocked.
