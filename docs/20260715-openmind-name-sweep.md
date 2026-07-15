# Openmind — name-collision sweep

Date: 2026-07-15
Scope: pre-launch check of the "Openmind" name for the open-source, self-hostable commonplace-book app, ahead of public GitHub launch.

## Methodology

Web searches only (no code/package installs), run 2026-07-15:

- `OpenMind AI robotics company funding`
- `github.com openmind repo`
- `npm package openmind`
- `PyPI openmind package`
- `"OpenMind" app bookmark read-later note-taking commonplace book`
- `USPTO trademark "OPENMIND" software class 9 42`
- `trademarkia OPENMIND trademark registration software`
- `"openmind.com" company product`

USPTO TESS and EUIPO eSearch have no public API and their search UIs were not directly reachable via web search or fetch tools in this environment. However, a concrete record was located via third-party TESS mirrors (uspto.report, trademarksoncall.com): **USPTO serial 99278181**, mark **"OPENMIND"**, filed by **Openmind Agi** (the robotics company), status **Non-Final Action (Office Action) mailed 2025-12-01**, covering SaaS software for facilitating machine-to-machine communication. This is a **live pending application, not a registration** — it has not (yet) matured to registered status, and it sits in a robotics/M2M-communication software sub-class, distinct from a commonplace-book/PKM app. It has not been independently re-confirmed directly on tsdr.uspto.gov/tmsearch.uspto.gov (both were unreachable in this environment); treat the mirror-sourced serial/status as reliable but not a substitute for a direct USPTO pull before any commercial trademark decision. No EUIPO record was located or checked in this sweep.

## Findings

| Name | Who | Space | Activity | Collision risk | Reason |
|---|---|---|---|---|---|
| OpenMind (OM1 / FABRIC) | OpenMind, Inc. (San Francisco; CEO Jan Liphardt), backed by Pantera Capital, Coinbase Ventures, DCG, etc. | Robotics / decentralised AI operating system for robots | Very active — $20M raise closed ~Aug 2025, active GitHub org (`github.com/OpenMind`, `OpenMind/OM1`), ongoing press coverage | Medium | Same exact name, well-funded and high-visibility in AI generally, but different product category (robot OS vs. personal knowledge/bookmarking app) and different technical audience — low chance of user confusion, but this is the org most likely to contest the name or rank above you in AI-related searches |
| OPEN MIND Technologies AG (hyperMILL) | OPEN MIND Technologies AG, Germany | CAD/CAM software for CNC milling | Long-established commercial vendor, own domain `openmind-tech.com`, likely holder of registered marks in software classes given decades on the market | Medium | Registered German/EU industrial-software company using "OPEN MIND" as a formal brand for decades — most likely of the group to actually hold class 9/42 trademarks, though its space (manufacturing CAM) is far from a PKM app |
| Openmind Technologies Inc. (staffing/consulting), Openmind Tech (openmindt.com), OpenMind Solutions LLC, OPENMINDS (behavioural health, openminds.com), OPENMIND (WPP Media agency) | Various small/mid businesses | IT staffing, digital consulting, healthcare market research, media/advertising | Active as businesses, low software-product visibility | Low | Different industries entirely (staffing, ad agency, healthcare consulting); minimal SEO or product overlap with a self-hosted PKM tool |
| GitHub orgs/repos: `openmind` (27 repos, robotics/telemetry, likely OpenMind Inc.'s org), `OpenMind-repo`, `openmind-consortium` (medical device / RC+S monitoring), `OpenMind/OM1`, `jaseg/openmind` (open-source EEG hardware), `Openmind-Research-Institute`, `ohhmm/openmind` (math solver framework), `spob/openmind`, `openminds` | Independent developers/orgs | Robotics, medical devices, brain-computer interfaces, math tooling | Mixed — some dormant, some active (OM1, consortium) | Low–Medium | No exact GitHub org/repo match found for a bookmarking/commonplace-book tool; `openmind` (singular, no hyphen) as a top-level GitHub org appears taken by OpenMind Inc.'s robotics project, which could force a naming variant (e.g. `open-mind`, matching this repo's actual folder name) for the GitHub org/repo slug |
| npm: `@openmind/zero` | Small/abandoned project | JS framework | Dormant — last published ~7 years ago | Low | Scoped package, inactive, no direct product overlap |
| PyPI: `openmind` (Ascend NPU fine-tuning suite), `openmind-hub`, `openmind-evaluate`, `openMINDS` (metadata schema), `openmindat` (geomaterial data) | Appears affiliated with the Ascend-NPU ecosystem (openmind.cn maintainers); openMetadataInitiative; geology tooling | ML tooling, metadata standards, scientific data | `openmind` on PyPI is actively maintained (v1.0.0, Jan 2025) | Medium | The bare `openmind` name is already taken and actively maintained on PyPI — if Openmind ever ships a Python package/SDK under that exact name, it will collide directly and need a different package name (e.g. `openmind-app`, `openmind-client`) |
| Commonplace-book / read-later / PKM space specifically (Notion, Obsidian, Evernote, Readwise, Screvi, mymind) | N/A | Closest functional competitor space | — | Low | No existing "OpenMind"-branded product found in the read-later/PKM/bookmarking category — this is the most important negative result: the specific niche this app is entering appears clear of the name |
| USPTO serial 99278181 "OPENMIND" — Openmind Agi (robotics co.) | OpenMind Inc./Agi | SaaS software, machine-to-machine communication | Pending — Non-Final Action (Office Action) mailed 2025-12-01, not yet registered | Medium | Confirmed via TESS mirrors (uspto.report, trademarksoncall.com) — a live pending US application for "OPENMIND," but still an application (facing an Office Action, so not guaranteed to register), and its covered sub-class (M2M/robotics SaaS) is distinct from a commonplace-book/PKM app. Direct tsdr.uspto.gov/tmsearch.uspto.gov confirmation and any EUIPO record still outstanding. |
| Other trademark coverage (USPTO/EUIPO, classes 9/42) | Not independently verified beyond the above | — | — | Unknown | Given OPEN MIND Technologies AG's decades-long commercial CAD/CAM software use, it is plausible it holds its own marks in software-related classes, but no serial/registration was located for it in this sweep. Should be verified directly on tmsearch.uspto.gov and euipo.europa.eu before any commercial (not just OSS) use, or before adopting the name as a formal registered trademark. |

## Overall risk read

For an **open-source, self-hosted, non-commercial-branded GitHub project** called "Openmind," the practical collision risk is **low-to-medium**:

- No direct competitor in the commonplace-book/PKM/bookmarking niche uses the name — the closest-confusion check came back clear.
- The main friction points are (1) the well-funded robotics company OpenMind Inc., which already holds the plain `github.com/openmind` GitHub org and dominates "OpenMind AI" search results, and (2) an actively maintained `openmind` PyPI package from the Huawei/Ascend ecosystem, which blocks that exact package name if this project ever ships a Python SDK.
- SEO/discoverability, not legal action, is the more likely real-world friction: "Openmind" search results are currently dominated by the robotics company's funding news, which could bury this project in search rather than trigger any formal dispute.
- Formal trademark risk is now partly concrete: OpenMind Inc./Agi has a **pending** (not yet registered) US application, serial 99278181, for "OPENMIND" covering M2M/robotics SaaS — a different sub-class from a commonplace-book app, and still subject to a pending Office Action, but it confirms the robotics company is actively pursuing exclusive rights to the exact word "OPENMIND" in software. This mildly raises (from "assumed" to "confirmed-pending") the odds that this project would eventually need a qualifier or rename if it ever sought its own registration in an adjacent class. A fuller register check (direct USPTO TESS/TSDR pull, plus EUIPO) is still advisable before any commercial use.

## Secondary launch surfaces (not swept)

This sweep covered software/product/trademark surfaces only. **Social handles (X/Twitter, GitHub org name availability beyond the repo slug, Bluesky, etc.) and domain availability (openmind.\* variants) were not checked** and remain open items for the maintainer to verify directly before public launch — availability on these surfaces changes quickly and is cheap to check first-hand at launch time.

## Recommendation

**Keep the name as-is for the OSS launch**, but **always pair it with a qualifier** in the README/tagline (e.g. "Openmind — the self-hosted commonplace book") to immediately disambiguate from the robotics company in search and in first impressions — this is now somewhat more strongly warranted given the robotics company's pending "OPENMIND" trademark application (serial 99278181), not just its market visibility. This is consistent with the repo's own framing ("Openmind (working title)" in CLAUDE.md), so there's already room to revisit later without this being treated as a final decision here.

Practical, low-cost mitigations regardless of the name decision:
- Use `open-mind` (already the repo/GitHub slug) rather than trying to claim the bare `openmind` GitHub org, which is unavailable and owned by the robotics company.
- If/when a Python package is published, avoid the bare name `openmind` on PyPI (already taken and active) — use something like `openmind-app` or a project-specific SDK name.
- Re-run a direct USPTO TESS / EUIPO eSearch query before any commercial trademark filing or paid product launch.

If a rename is later considered (e.g. to reduce SEO competition with the robotics company), sanity-checked candidate alternatives worth a follow-up sweep:
- **Marginalia** — commonplace-book connotation (marginal notes), no widely-known major software collision spotted in this sweep (not independently verified further).
- **Waypost** / **Wayfare** — evokes "finding things by fragments," no major AI/software brand recognised in this sweep.
- **Commonplace** (or **Commonplace.app**) — on-the-nose descriptive name; likely already used casually by others in the note-taking blogging space (see "digital commonplace book" search results above), so this would need its own dedicated collision sweep before adoption.

These are sanity-checked only against the searches run above, not independently swept in the same depth as "Openmind" itself.

## Note

This is research, not legal advice. No rename decision is made in this document — it is an input for the maintainer's own judgement, and any trademark conclusions should be confirmed via a direct USPTO TESS / EUIPO eSearch query (or counsel) before relying on them for a commercial launch.

## Addendum (2026-07-15): social handles and domains

Follow-up sweep closing the "Secondary launch surfaces (not swept)" item above. Web-search-only, best-effort (no whois/registrar API access, no authenticated X/Reddit/Product Hunt account lookups) — several entries could not be conclusively confirmed either way and are marked accordingly.

| Surface | Status | Evidence/note |
|---|---|---|
| X/Twitter `@openmind` (bare) | Likely taken/reserved | No live profile surfaced directly under this exact handle in search, but the robotics company's own handle `@openmind_agi` and numerous close variants (`@OpenMindinc`, `@openmind_mag`, `@openmind_group`, `@OpenMindCenter`, `@openmindtv`, `@betheopenmind`) are active — bare `@openmind` was not independently confirmed as free or suspended; treat as **unverified, assume taken/high-risk** given the density of adjacent active accounts. |
| X/Twitter `@openmindapp` | Occupied (closely) | No exact `@openmindapp` account confirmed, but `twitter.com/OpenMind_App` ("OpenMind — Social Media ReImagined") is an active-looking account with a near-identical handle, which would likely cause confusion/collision even if the exact string is technically free. |
| X/Twitter `@getopenmind` | No evidence of an active account | Searches for `"@getopenmind"` returned no matching profile — closest results were unrelated Openmind-branded accounts (`@openmindsf`, `@openmindsq`, `@openmindscircle`). Best-effort only; not confirmed via a direct handle-availability check (X has no public registration-status API reachable here). |
| Reddit r/openmind | Exists, activity unconfirmed | Search confirms r/openmind exists as a subreddit but returned no detail on its content, size, or activity level — could not verify if it's active, dormant, or squatted. Best-effort limit: Reddit's search surfaced no crawlable subreddit page content. |
| Product Hunt "OpenMind" | Taken (multiple) | At least two live PH listings under variants of the name: an "OpenMind" mentorship platform (`producthunt.com/products/openmind`) and an "Open Mind" diagram tool (`producthunt.com/products/open-mind`) — the bare "OpenMind" product slug is occupied on Product Hunt. |
| Mastodon `openmind` | Not found / unconfirmed | No Mastodon-specific results surfaced in this sweep; no dedicated Mastodon search was run beyond general web search, which returned nothing relevant. Treat as unverified — recommend a direct instance search (e.g. mastodon.social, fosstodon.org) before launch. |
| Bluesky `openmind` / `openmind.bsky.social` | Not found / unconfirmed | Web search surfaced no `openmind`-handled Bluesky profile (only an unrelated `@naturistian.bsky.social` whose bio happens to contain the phrase "open mind"). Bare handle appears plausibly available but this was not confirmed via a direct bsky.app handle check, which the search tool cannot perform. |
| Domain: `openmind.app` | Unconfirmed | No whois/registrar evidence surfaced either way; search results returned only generic whois-tool pages and unrelated `.app`-domain guidance, not a record for this specific domain. Recommend a direct registrar lookup (e.g. Namecheap/GoDaddy availability check) before launch. |
| Domain: `openmind.dev` | Unconfirmed, likely taken or forwarding | No direct whois hit, but multiple `dev.*` subdomains of unrelated OpenMind-branded products appeared (`dev.openmind-tech.com`, `dev.openmindpro.com`), suggesting the `openmind.dev`/`.dev`-adjacent space is crowded even if the exact apex domain's registration status wasn't confirmed. |
| Domain: `openmind.so` | Unconfirmed | No search evidence found either way; not independently checked against a registrar. |
| Domain: `getopenmind.com` | Unconfirmed | No search evidence found either way; not independently checked against a registrar. |
| Domain: `openmindapp.com` | Unconfirmed, but adjacent `.org` is taken | `openmindapp.com` itself returned no evidence, but `openmindapp.org` is live and in active use as an open-source guided-meditation app (`github.com/ryanallen/openmindapp`, `openmindapp.org`) — a directly adjacent TLD collision under the near-identical name, though not the `.com` itself. |
| (Reference) Domain: `openmind.com` | Confirmed taken, occupied by the robotics company | `openmind.com` and `docs.openmind.com` are live and operated by OpenMind Inc./Agi (the robotics company already flagged as the main collision risk in the main sweep above) — not one of the requested variants, but confirms the most obvious apex domain is unavailable. |

This reinforces the main sweep's conclusion: the bare `openmind` string is crowded across every surface checked (X handles, Product Hunt, and the `.com`/`.org` domain space), so launch messaging should lean on a **qualifier-style handle** (e.g. `@openmindhq`, `@openmind_pkm`, or similar) rather than fighting for `@openmind`/`@openmindapp`, which are either occupied or sit too close to existing active accounts. `@getopenmind` on X and the `.so`/`getopenmind.com` domains are the most promising unconfirmed-but-plausible options and are worth a direct first-hand check (registrar + platform signup attempt) before committing, since several entries above could not be verified beyond search-engine visibility in this environment.

## Sources

- [OpenMind raises $20M to connect intelligent machines - The Robot Report](https://www.therobotreport.com/openmind-raises-20m-connect-intelligent-machines/)
- [Blog - Investing in OpenMind - Pantera Capital](https://panteracapital.com/blog-investing-in-openmind/)
- [OpenMind Secures $20 Million for Robotic Intelligence Infrastructure](https://theaiinsider.tech/2025/08/04/openmind-secures-20-million-for-robotic-intelligence-infrastructure/)
- [Pantera leads $20 million raise for OpenMind's decentralized operating system for robots | The Block](https://www.theblock.co/post/365312/pantera-leads-20-million-raise-for-openminds-decentralized-operating-system-for-robots)
- [OpenMind (company news)](https://openmind.com/news/openmind-20m-raise)
- [OpenMind · GitHub](https://github.com/openmind)
- [GitHub - OpenMind/OM1](https://github.com/OpenMind/OM1)
- [OpenMind Consortium · GitHub](https://github.com/openmind-consortium)
- [GitHub - jaseg/openmind](https://github.com/jaseg/openmind)
- [GitHub - ohhmm/openmind](https://github.com/ohhmm/openmind)
- [@openmind/zero - npm](https://www.npmjs.com/package/@openmind/zero)
- [openmind · PyPI](https://pypi.org/project/openmind/)
- [openmind-hub · PyPI](https://pypi.org/project/openmind-hub/0.7.0/)
- [openmind-evaluate · PyPI](https://pypi.org/project/openmind-evaluate/)
- [openMINDS · PyPI](https://pypi.org/project/openMINDS/)
- [openmindat · PyPI](https://pypi.org/project/openmindat/)
- [CAD CAM software | OPEN MIND (openmind-tech.com)](https://www.openmind-tech.com/en/)
- [Openmind Tech (openmindt.com)](https://www.openmindt.com/)
- [OPENMIND (part of WPP Media) - LinkedIn](https://www.linkedin.com/company/openmindworld)
- [OPEN MIND Technologies - Crunchbase](https://www.crunchbase.com/organization/open-mind-technologies)
- [Openmind Technologies Inc Reviews - G2](https://www.g2.com/products/openmind-technologies-inc/reviews)
- [OPEN MINDS (openminds.com)](https://openminds.com/)
- [OpenMind Solutions LLC](https://www.openmindsolutions.com/)
- [Trademarkia](https://www.trademarkia.com/)
- [OPENMIND - Openmind Agi Trademark, serial 99278181 - uspto.report](https://uspto.report/TM/99278181)
- [Trademarks On Call : OPENMIND, serial 99278181](https://trademarksoncall.com/trademark/openmind/99278181)
