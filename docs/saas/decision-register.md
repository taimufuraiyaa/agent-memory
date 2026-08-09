# SaaS Decision Register

This register separates approved product direction from decisions that require
a named business, legal, security, or operations owner. An unchecked decision
is a release dependency, not permission for engineering to guess.

| Decision | Status | Current direction | Required owner/evidence |
|---|---|---|---|
| MVP audience | Approved | Individual, private-only accounts; no sharing or publishing | Product approval recorded in the SaaS requirements |
| Original retention | Approved in principle | Encrypted originals retained until deletion or scoped legal hold | Privacy copy and counsel review remain required |
| Rights confirmation | Approved | First use and every 30 days; per-source rights basis; no copyright-verification claim | Implemented local behavior; hosted parity remains |
| Launch countries | Open | No country is inferred from developer or user location | Product and counsel: country list, minimum age, notice venue, support language |
| Managed identity | Open | Standards-based verified identity behind an adapter | Security/vendor review and exit plan |
| Infrastructure ownership | Approved | Development, staging, and production are self-managed; no external cloud-provider deployment | Internal DevOps, operations, security, capacity, recovery, and topology evidence |
| Billing and email | Open | Provider-neutral event and entitlement boundaries | Product/vendor review and sandbox credentials |
| Model providers | Open | Tenant-aware gateway; no customer content in general logs or shared training | Privacy/security/provider contract review |
| Trial and paid limits | Open | Bounded trial and one individual plan | Product pricing and unit-economics model |
| Backup deletion window | Open | Tombstones replay before restored data is served | Operations test plus counsel/privacy approval |
| Audit retention | Open | Content-free hot search plus tamper-resistant archive | Security and counsel approval |
| Retrieval parity threshold | Open | Fixed local-versus-hosted benchmark blocks cutover | Product/engineering threshold approval |

Engineering may build self-managed interfaces and local emulators while these
items are open. Production integrations, legal text, and rollout gates
must not be marked complete until their named evidence exists.

Internal operators may record the monthly infrastructure operations budget and
its assumption status under System → Infrastructure. These installation-scoped
planning values do not deploy infrastructure, authorize spending, create a
customer charge, or replace release evidence.
