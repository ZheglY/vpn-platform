# Stage 9 Legal, Payment, Privacy, and Provider Checklist

Review date: 2026-07-26  
Next scheduled review: 2026-08-26, or immediately after any jurisdiction,
provider, product, or data-use change  
Owner of final launch decision: Product owner  
Legal status: working checklist only; **not legal advice**  
Decision: **NO-GO for real sales and real customer records**

## Operating restriction

Until every launch-blocking row is `resolved` with dated evidence:

- YooKassa remains sandbox-only;
- no real payment or production provider credential may be used;
- no real customer record may be collected;
- no public sale, production VPN node, or customer traffic is authorized.

`blocked` means an owner decision/evidence is missing. `requires_counsel` means a
qualified professional must determine applicability and the required control.
Unknown values are never filled with assumptions.

## Checklist

| ID | Decision required | Status | Owner | Evidence | Checked | Next review | Launch blocking |
|---|---|---|---|---|---|---|---|
| LEG-01 | Seller country of registration | blocked | Product owner | No approved seller identity/country in repository | 2026-07-26 | 2026-08-26 | yes |
| LEG-02 | Countries in which buyers may be accepted | requires_counsel | Product owner / Legal | No approved country allow/deny policy | 2026-07-26 | 2026-08-26 | yes |
| LEG-03 | Countries for control plane and VPN nodes | requires_counsel | Legal / Operations | No provider or region selected | 2026-07-26 | 2026-08-26 | yes |
| LEG-04 | Legality and restrictions for VPN/proxy sale and operation in every relevant country | requires_counsel | Legal | No jurisdictional opinion | 2026-07-26 | 2026-08-26 | yes |
| LEG-05 | Legal entity, sole proprietor, or other seller form | requires_counsel | Product owner / Legal | No approved seller entity | 2026-07-26 | 2026-08-26 | yes |
| LEG-06 | Tax registration, accounting, settlement, and reporting | requires_counsel | Finance / Tax counsel | No approved tax/accounting model | 2026-07-26 | 2026-08-26 | yes |
| PAY-01 | Executed YooKassa merchant agreement for the seller and product | blocked | Product owner / Finance | Sandbox integration only; no production agreement | 2026-07-26 | 2026-08-26 | yes |
| PAY-02 | YooKassa permits the VPN/proxy product and intended buyer/region model | requires_counsel | Legal / Finance | Provider approval and prohibited-use assessment absent | 2026-07-26 | 2026-08-26 | yes |
| PAY-03 | 54-FZ applicability and fiscalization method | requires_counsel | Tax counsel / Finance | YooKassa documents receipt options; seller/applicability unknown | 2026-07-26 | 2026-08-26 | yes |
| PAY-04 | VAT/NDS rate, payment subject/method, item description, and buyer receipt fields | requires_counsel | Tax counsel / Finance | Billing contract has no approved production receipt schema | 2026-07-26 | 2026-08-26 | yes |
| PAY-05 | Buyer email/phone collection and lawful handling when required for receipts | requires_counsel | Privacy / Finance | Current product does not collect an approved receipt contact | 2026-07-26 | 2026-08-26 | yes |
| PAY-06 | Receipt creation for payment, cancellation, and refund, including failure reconciliation | requires_counsel | Finance / Billing | Sandbox payment/refund state exists; fiscal receipt workflow absent | 2026-07-26 | 2026-08-26 | yes |
| PAY-07 | Refund and cancellation eligibility, timing, proration, and support authority | requires_counsel | Product owner / Legal | Technical full-refund facts exist; customer policy absent | 2026-07-26 | 2026-08-26 | yes |
| PAY-08 | Consumer protection, service description, availability, complaints, and remedies | requires_counsel | Legal / Support | No approved consumer-facing terms | 2026-07-26 | 2026-08-26 | yes |
| PAY-09 | Terms of service acceptance, versioning, withdrawal, and evidence | requires_counsel | Product owner / Legal | Identity stores accepted terms version; production terms do not exist | 2026-07-26 | 2026-08-26 | yes |
| PRIV-01 | Approved privacy policy and data inventory | requires_counsel | Privacy / Legal | Threat model describes minimization; policy is not approved | 2026-07-26 | 2026-08-26 | yes |
| PRIV-02 | GDPR/ePrivacy and other privacy-law applicability | requires_counsel | Privacy counsel | Buyer, seller, hosting, and transfer countries unknown | 2026-07-26 | 2026-08-26 | yes |
| PRIV-03 | Lawful basis for each personal-data purpose | requires_counsel | Privacy counsel / Data owners | No approved record of processing activities | 2026-07-26 | 2026-08-26 | yes |
| PRIV-04 | Controller, joint-controller, and processor roles | requires_counsel | Privacy counsel | Telegram, YooKassa, VPS, and observability roles not classified | 2026-07-26 | 2026-08-26 | yes |
| PRIV-05 | Data-subject access/export identity verification and response | blocked | Privacy / Support | Support-safe reads exist; end-to-end procedure absent | 2026-07-26 | 2026-08-26 | yes |
| PRIV-06 | Deletion workflow, exceptions, and owner-by-owner completion proof | requires_counsel | Privacy / Data owners | Bounded retention exists; legal policy and DSAR orchestration absent | 2026-07-26 | 2026-08-26 | yes |
| PRIV-07 | Mandatory and permissible retention per dataset | requires_counsel | Legal / Finance / Privacy | Stage 8 periods are local defaults, not production policy | 2026-07-26 | 2026-08-26 | yes |
| PRIV-08 | Breach detection, assessment, notification deadlines, and contacts | requires_counsel | Security / Privacy / Legal | Technical incident response drafted; legal notification matrix absent | 2026-07-26 | 2026-08-26 | yes |
| PRIV-09 | Subprocessor register, agreements, transfer mechanisms, and change notice | requires_counsel | Legal / Privacy | Providers have not been selected | 2026-07-26 | 2026-08-26 | yes |
| TG-01 | Telegram Bot Platform Developer Terms acceptance and continuing compliance owner | requires_counsel | Product owner / Legal | Official terms identified; no dated organizational acceptance | 2026-07-26 | 2026-08-26 | yes |
| TG-02 | Bot privacy disclosure, independent seller identity, support and refund presentation | requires_counsel | Product owner / Legal / Support | Bot is technically implemented; approved user text/policy absent | 2026-07-26 | 2026-08-26 | yes |
| VPS-01 | VPS provider AUP permits VPN/proxy and public exit traffic | blocked | Operations / Legal | No provider selected or written approval | 2026-07-26 | 2026-08-26 | yes |
| VPS-02 | Regions, sanctions, export controls, and geographic restrictions | requires_counsel | Legal / Operations | No approved provider/region/customer matrix | 2026-07-26 | 2026-08-26 | yes |
| OPS-LEGAL-01 | Public acceptable-use policy for customers | requires_counsel | Product owner / Legal / Abuse | Draft operational runbook is not a customer agreement | 2026-07-26 | 2026-08-26 | yes |
| OPS-LEGAL-02 | Monitored abuse contact, triage authority, evidence minimization, and escalation | blocked | Abuse / Operations / Legal | Owner and real intake channel not assigned | 2026-07-26 | 2026-08-26 | yes |
| OPS-LEGAL-03 | Lawful-request receipt, validation, preservation, challenge, response, and audit | requires_counsel | Legal / Security | No approved jurisdictional process | 2026-07-26 | 2026-08-26 | yes |
| OPS-LEGAL-04 | Copyright complaint and repeat-abuse process | requires_counsel | Legal / Abuse | No approved process | 2026-07-26 | 2026-08-26 | yes |
| OPS-LEGAL-05 | Customer support channels, hours, response objectives, and refund escalation | blocked | Product owner / Support | No production support organization or channel | 2026-07-26 | 2026-08-26 | yes |
| OPS-LEGAL-06 | Minimum age and age-assurance requirements by buyer country | requires_counsel | Legal / Product owner | Buyer countries and legal basis unknown | 2026-07-26 | 2026-08-26 | yes |

## Official source material

These sources identify provider requirements to be assessed; they do not answer
the unresolved jurisdiction/product questions:

- YooKassa API and webhook behavior:
  <https://yookassa.ru/developers/using-api/webhooks>
- YooKassa receipt/fiscalization overview:
  <https://yookassa.ru/developers/payment-acceptance/receipts/basics>
- YooKassa 54-FZ receipt option and buyer email:
  <https://yookassa.ru/developers/payment-acceptance/receipts/54fz/yoomoney/basics>
- YooKassa sandbox and receipt testing:
  <https://yookassa.ru/developers/payment-acceptance/testing-and-going-live/testing>
- Telegram Bot Platform Developer Terms:
  <https://telegram.org/tos/bot-developers>
- Telegram Bot API webhook authentication:
  <https://core.telegram.org/bots/api>

Provider terms and legislation can change. Legal and provider owners must
re-check the current official text on every review date and preserve dated
approval evidence outside the repository when it contains confidential data.

## Closure rule

A row becomes `resolved` only when the named owner attaches a dated decision,
professional advice where required, approved user/provider documents, and a
testable operational control. A link to documentation alone is not resolution.
