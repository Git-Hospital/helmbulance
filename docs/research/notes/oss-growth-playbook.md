# OSS growth playbook for bug-finding / upstream-fix tools (as of 2026-10-11)

Method note: web searches via a summarising search tool plus a few page fetches; GitHub star counts pulled live with `gh api repos/OWNER/REPO` on 2026-10-11. Several claims come from secondary summaries and are flagged. Nothing was posted, starred or opened.

## 1. Case studies: tools that grew by finding/fixing bugs in other projects

### Takeaway
The tools that became famous attached a visible, countable proof (OSS-Fuzz: 10,000+ vulnerabilities; Coccinelle: 400+ accepted kernel patches; Rogers/curl: ~50 merged fixes) to a free service or a respected maintainer-facing workflow. Of the k8s linters, stars cluster at 2.5k-6k (niche tools) with Trivy (38k) and Kubescape (11.8k) as outliers backed by a vendor and, for Kubescape, CNCF. Datree shows that a popular linter (6.3k stars) can still die when the company dies.

### Cited Findings
- OSS-Fuzz launched 2016 after Heartbleed; docs state over 10,000 vulnerabilities and over 36,000 bugs found and fixed across about 1,000 projects (as of August 2023) — [OSS-Fuzz docs](https://google.github.io/oss-fuzz/)
- OSS-Fuzz had found 150 bugs at its Dec 2016 launch and over 1,000 (a quarter potential security vulnerabilities) five months later — [Threatpost](https://threatpost.com/googles-oss-fuzz-finds-1000-open-source-bugs/125545), [Google OS blog](https://opensource.googleblog.com/2016/12/announcing-oss-fuzz-continuous-fuzzing.html)
- OSS-Fuzz is a service projects opt into (bugs go to project members privately, then public after the deadline); this opt-in model is why it avoids the "unsolicited reports" backlash — [bug disclosure guidelines](https://google.github.io/oss-fuzz/getting-started/bug-disclosure-guidelines/)
- Coccinelle: development began 2006, first release 2008; ~180 accepted kernel patches (LWN, ~2009), 400+ by FOSDEM 2010. Designed by Julia Lawall et al. Semantic patches are now shipped inside the kernel tree (Documentation/dev-tools/coccinelle) — [LWN](https://lwn.net/Articles/315686/), [FOSDEM 2010](https://archive.fosdem.org/2010/schedule/events/coccinelle.html), [kernel docs](https://docs.kernel.org/_sources/dev-tools/coccinelle.rst.txt), [Lawall talk 2018](https://events19.linuxfoundation.org/wp-content/uploads/2017/12/Coccinelle-10-Years-of-Automated-Evolution-and-Bug-Finding-in-the-Linux-Kernel-Julia-Lawall-Inria.pdf)
- GitHub Security Lab / CodeQL: multi-repository variant analysis (MRVA, March 2023) runs CodeQL queries on up to 1,000 repos via Actions; Security Lab used it for Android advisories affecting "over 10 million applications"; public proof page is the CodeQL "Wall of Fame" + GitHub Advisory Database — [GitHub blog](https://github.blog/security/vulnerability-research/multi-repository-variant-analysis-a-powerful-new-way-to-perform-security-research-across-github/), [docs](https://docs.github.com/en/code-security/concepts/code-scanning/multi-repository-variant-analysis)
- OpenSSF Scorecard: Google's team regularly scans 1.2 million repos and publishes results; fix PRs were sent by third-party StepSecurity (300+ repos helped, Sept 2024 figure) not Scorecard itself — [OpenSSF blog 2024-03](https://openssf.org/blog/2024/03/05/openssf-scorecard-evaluating-and-improving-the-health-of-critical-oss-projects/), [StepSecurity](https://stepsecurity.io/blog/secure-software-development-stepsecuritys-role-in-increasing-openssf-scorecard-scores)
- Dependabot/Renovate: the lesson is noise control. GitHub itself recommends grouped, slower-cadence PRs because "individually helpful, collectively noise"; Renovate offers prHourlyLimit/prConcurrentLimit — [GitHub blog 2024-04](https://github.blog/security/supply-chain-security/tame-dependabot-group-your-updates-slow-the-cadence-keep-security-fast/), [comparison](https://appsecsanta.com/dependabot-vs-renovate)
- Datree: GitHub repo has 6,329 stars, archived, last push 2024-04-23. README says the commercial company closed (a startup database says July 2023); offline mode still works but registry/dashboard are gone. Acquisition claims conflict between sources — [repo via gh API](https://github.com/datreeio/datree), [aggregator listing](https://finder.startupnationcentral.org/company_page/datreeio). I did not find an official shutdown post.
- Kubescape: created 2021 by ARMO; CNCF Sandbox Dec 13 2022 (CNCF page; ARMO PR Jan 2023, CNCF blog says Nov 2022); Incubating Feb 2025 (about 2 years 2 months later) — [CNCF blog](https://www.cncf.io/blog/2025/02/26/kubescape-becomes-a-cncf-incubating-project/), [SD Times](https://sdtimes.com/kubernetes/security-platform-kubescape-accepted-into-cncf-sandbox), [InfoQ](https://www.infoq.com/news/2025/03/cncf-kubescape-incubating/)
- Artifact Hub scans charts: it extracts images from a dry-run render with default values (or `artifacthub.io/images` annotation) and Trivy-scans latest version daily, older weekly — an existing channel where chart quality is already shown — [Artifact Hub docs](https://artifacthub.io/docs/topics/security_report)
- Live GitHub stars on 2026-10-11 (created date in parentheses; via `gh api`): oss-fuzz 12,718 (2016); trivy 38,337 (2019); kubescape 11,790 (2021); semgrep 16,960 (2019); renovate 22,717 (2016); ossf/scorecard 5,748 (2020); github/codeql 10,186; kube-bench 8,221 (2017); kube-linter 3,524 (2020, stackrox); polaris 3,393 (2018); kubeconform 3,218 (2020); kube-score 3,103 (2018); pluto 2,588 (2020); helm/chart-testing 1,645 (2017); artifact hub 2,108 (2020); nova 877 (2020); datree 6,329 (2021, archived); kind 15,540 (2018); helm 30,308 (2015); bitnami/charts 10,415; prometheus-community/helm-charts 6,216. Sources: GitHub API (e.g. https://api.github.com/repos/FairwindsOps/pluto).
- Show HN history for k8s/Helm tools is mostly tiny: Polaris (2019) 2 pts, Kubescape 2021 2 pts, "Chart Preview" for Helm (Dec 2025) 22 pts, "Show HN: Renovate Kubernetes-native" (Feb 2026) 43 pts, "Kubernetes Spec Explorer" (Dec 2024) 216 pts. HN is not a reliable k8s-tool channel — [HN Algolia search](https://hn.algolia.com/api/v1/search?query=helm%20chart&tags=show_hn)

### Inferences
- Niche k8s static checkers (kube-linter, polaris, kube-score, pluto) reached only 2.5k-3.5k stars in 5-8 years even with Fairwinds/Red Hat behind them; "famous" for a 2-person team realistically means low thousands of stars plus mentions in docs of upstream projects, not Trivy-scale.
- Strongest differentiator vs these tools: they are static; a "reproduced on a real cluster" claim is closer to OSS-Fuzz's "crash with reproducer" proof than to a linter warning.
- Datree is the cautionary tale for a plan that depends on a commercial entity; the OSS repo kept stars but no maintenance.

### Gaps
- Star-history curves (time to 1k/5k stars) for Trivy, Pluto, kube-linter, Polaris: not retrieved (needs star-history API/GraphQL pass).
- No sourced account of Trivy's, Pluto's, Polaris', Nova's or kube-linter's first-user channel; "chart-doctor"-style tools: none identified.
- Dependabot/Renovate adoption numbers and PR-etiquette-from-maintainer-side sources: only the noise-control guidance found.
- Current OSS-Fuzz totals post Aug 2023 not found.
- Scorecard "fix PR" count not found.

## 2. Failures and backlash; 2025-2026 AI contribution policies; etiquette

### Takeaway
Maintainers reject volume without understanding: curl ended its bounty (Jan 2026) after AI slop, FFmpeg called Google's AI-found bugs "CVE slop", Hacktoberfest 2020 and UMN 2021 show the cost of unsolicited or non-consensual work. But the same maintainers praise AI-assisted findings that are verified and come from a skilled human (Rogers/curl: ~50 merged fixes). Kubernetes (Apr 2026) requires disclosure and human accountability.

### Cited Findings
- curl ended its bug bounty at the end of Jan 2026; Stenberg's stated goal: "remove the incentive for people to submit crap and non-well researched reports to us. AI generated or not." Quote: "you should NEVER report a bug or a vulnerability unless you actually understand it – and can reproduce it." The Register reports 7 submissions in one week, none a vulnerability — [The Register 2026-01-21](https://www.theregister.com/2026/01/21/curl_ends_bug_bounty/)
- Reports moved from HackerOne to GitHub from Feb 1 2026; a secondary source claims only 5% of 2025 reports were real and 3-4 reviewers per report — [BleepingComputer](https://bleepingcomputer.com/news/security/curl-ending-bug-bounty-program-after-flood-of-ai-slop-reports/), [Hackmag](https://hackmag.com/news/bug-bounty-curl). A Pinggy post claims curl paused reports 1 Jul-3 Aug 2026; single source, unverified — [Pinggy](https://pinggy.io/amp/blog/curl_ai_slop_summer_of_bliss/)
- Counter-example: Joshua Rogers sent curl a large list from AI-assisted scanners (ZeroPath, Almanax, Corgea, Gecko, Amplify); Stenberg called it "truly awesome", about 50 bugfixes merged, curl dropped krb5-ftp support — [Simon Willison 2025-10-02](https://simonwillison.net/2025/Oct/2/curl/), [Slashdot](https://developers.slashdot.org/story/25/10/12/0619247/ai-slop-not-this-time-ai-tools-found-50-real-bugs-in-curl), [opensourcesecurity.io](https://opensourcesecurity.io/2025/2025-10-ai-joshua-rogers/)
- FFmpeg vs Google (Nov 2025): Big Sleep found a bug in a 1995 game codec; FFmpeg called it "CVE slop" and asked Google to fund the project or stop sending AI-found bugs. Project Zero's Reporting Transparency trial (July 2025) flags new reports publicly in about a week with the 90-day clock. Big Sleep had reported 20 flaws, mostly FFmpeg and ImageMagick — [It's FOSS](https://itsfoss.com/news/ffmpeg-google-fiasco/), [The New Stack](https://thenewstack.io/ffmpeg-to-google-fund-us-or-stop-sending-bugs/), [Computing](https://www.computing.co.uk/news/2025/security/big-sleep-finds-20-flaws-in-open-source-software)
- Hacktoberfest 2020: DigitalOcean said at least 4% of PRs were invalid/spam; switched to opt-in (repo topic `hacktoberfest` or maintainer label `hacktoberfest-accepted`) from Oct 3 2020; review window cut 14 to 7 days in 2021 — [The Register 2020-10-01](https://www.theregister.com/2020/10/01/digitalocean_hacktoberfest_pull_request_spam/), [DevRel case study](https://developerrelations.com/case-studies/overcoming-hacktoberfest-spam), [Drew DeVault](https://hacktivis.me/git/mirror/drewdevault.com/file/content/blog/Spamtoberfest.gmi.html)
- UMN "hypocrite commits": Feb 2021 paper, April 2021 Kroah-Hartman banned UMN, vowing to revert past commits; the trigger was patches from a "new static analyzer" sent without warning; Linux TAB later published its audit and recommendations. Lesson for tool authors: bot/tool-generated patches must be labelled as such and honest — [It's FOSS](https://itsfoss.com/news/hypocrite-commits/), [Phoronix](https://www.phoronix.com/news/Hypocrite-Commit-Open-Letter), [SD Times](https://sdtimes.com/security/umn-security-researchers-apologize-to-the-linux-community/)
- Kubernetes AI policy: Steering Committee announced April 10 2026: AI-assisted PRs allowed if author understands every change; disclose in PR description; no AI co-author/assisted-by trailers; CLA requires a human. Blog 2026-06-26 "Open source maintainership in the age of AI" (Kevin Hannon, Red Hat) — [dev list announcement](https://groups.google.com/a/kubernetes.io/g/dev/c/7Y9016gdFZw/m/NQbEfZQvAgAJ), [k8s blog](https://kubernetes.io/blog/2026/06/26/open-source-maintainership-in-the-age-of-ai/), [InfoQ](https://infoq.com/news/2026/07/kubernetes-ai-policy). I only read the opening of the blog post (page truncated).
- Gentoo (April 2024 council vote) and NetBSD (May 2024, LLM output "tainted") ban AI-assisted code; QEMU's written policy declined AI content, with a Bonzini proposal to relax; whether QEMU adopted it is unverified — [Linuxiac NetBSD](https://linuxiac.com/netbsd-new-policy-prohibits-usage-of-ai-code/), [The Register](https://www.theregister.com/a/5248638), [SSD Nodes guide](https://www.ssdnodes.com/learn/ai-assisted-code-open-source-policies)
- Etiquette that Stenberg and others state: understand and reproduce before reporting (above); GitHub's private reporting form asks for summary, details, proof of concept, impact, and has an optional "used AI assistance" flag — [GitHub docs](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)

### Inferences
- Helmbulance's "cluster-reproduced only" rule matches exactly what Stenberg demands; it is the product's moral license. Publish the reproducer with every report.
- Upstream chart PRs should disclose the tool and any AI use in the PR body (Kubernetes-style), come after an issue, be one fix per PR, and include a failing-then-passing scenario.
- Mass campaigns across many unrelated chart repos risk the FFmpeg/Hacktoberfest reaction; throttle (few per week), opt-in where possible, and make the first batch to charts whose maintainers invited it.

### Gaps
- Helm project, Artifact Hub, bitnami, prometheus-community: whether they have written AI/bot contribution rules was not checked; look at each CONTRIBUTING.md before sending anything.
- CNCF-level AI policy (beyond Kubernetes) not found.
- Node/other bot bans: not researched.
- Kubernetes blog's concrete numbers not read (truncated).

## 3. Responsible disclosure norms

### Takeaway
90 days is the de facto clock (Google/OSS-Fuzz), with a 14-day grace if a fix date is committed; GitHub private vulnerability reporting (PVR) exists only where maintainers enabled it. Helm-chart misconfigurations are mostly ordinary bugs, so public issue/PR is the default; reserve private reporting for ones with clear security impact.

### Cited Findings
- OSS-Fuzz: reports go to project members; made public after 90 days or when the fix is released, whichever is earlier; weekend deadlines move to the next workday; 14-day grace period if upstream commits before the deadline to a patch date within 14 days — [OSS-Fuzz guidelines](https://google.github.io/oss-fuzz/getting-started/bug-disclosure-guidelines/). A secondary source adds a "30 days after fix" rule; the primary page I fetched does not state it — [Threatpost-era summary via search](https://lwn.net/Articles/710534)
- GitHub PVR: reporter fills a form, becomes collaborator/credited user on the advisory; maintainers must enable it; separate from SECURITY.md; if disabled, follow the security policy or ask for a contact via issue — [GitHub docs](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
- Project Zero's 2025 Reporting Transparency trial and the FFmpeg reaction show that even a standard 90-day policy generates backlash when the reporter is a large firm and the bug is low-impact — [It's FOSS](https://itsfoss.com/news/ffmpeg-google-fiasco/)

### Inferences
- Suggested triage (a guess, not sourced): class A security-impacting (e.g. secret leaked in rendered output, network isolation silently absent) via PVR/SECURITY.md with a 90-day clock; class B operational (upgrade without values fails, immutable field error) as a public issue with a reproducer.
- Never publish an A-class finding on the findings page before the fix or deadline.

### Gaps
- No authoritative source on when a Helm misconfiguration counts as a CVE-worthy vulnerability (CNA rules, Kubernetes SRC policy) was found.
- GitHub's default for PVR enablement on community charts not measured.

## 4. Launch and distribution channels

### Takeaway
KubeCon EU 2027 CFP closes 2026-10-11 23:59 CEST (today); NA 2027 CFP not yet published (2026 pattern: opened Apr 6, closed May 31). CNCF Sandbox is cheap to apply for but gives little except credibility. HN is hit and miss for k8s tools.

### Cited Findings
- KubeCon + CloudNativeCon Europe 2027: Barcelona, 15-18 March 2027. CFP deadline Sunday 11 Oct (2026) 11:59 PM CEST, notifications Mon 7 Dec, schedule Wed 9 Dec. Formats: 30-min session, 30-min panel, 5-min lightning talk, digital poster, 80-min tutorial; first-time speakers encouraged; submitters list any CNCF sandbox/incubating/graduated software; no eligibility rule for sandbox — [LF CFP page](https://events.linuxfoundation.org/kubecon-cloudnativecon-europe/program/cfp/), [Sessionize](https://sessionize.com/kubecon-cloudnativecon-europe-2027/)
- Maintainer Summit EU 2027 on 14 March 2027 is limited to TOC reps, graduated/incubating maintainers, TAG/WG leads - not Sandbox — [Sessionize](https://sessionize.com/maintainer-summit-eu-2027/)
- KubeCon NA 2027: 8-11 Nov 2027, New Orleans (third-party listing); no official CFP dates found. 2026 NA pattern: CFP open April 6, close May 31, notification Aug 3 — [beri.net](https://www.beri.net/events/kubecon-cloudnativecon-na-2027), [Sessionize NA 2026](https://sessionize.com/kubecon-cloudnativecon-north-america-2026/)
- CNCF Sandbox: apply via issue form in the Sandbox Applications repo; TOC reviews; requires license compliance, repo, roadmap, contribution guide, code of conduct, comparison with similar projects; sandbox is "early-stage, warrants experimentation", no due diligence; CNCF-hosted in-person co-located events are for Incubating+/not Sandbox (2026 guidelines) — [CNCF lifecycle](https://contribute.cncf.io/projects/lifecycle/), [2026 event guidelines PDF](https://events.linuxfoundation.org/wp-content/uploads/2026/02/2026-cncf-project-and-co-located-event-guidelines-2-5-26.pdf), [devclass](https://devclass.com/?p=3003)
- Google Summer of Code 2026: 185 mentoring orgs; org applications Jan 19-Feb 3, contributor applications Mar 16-31, results ~Apr 30, coding from May 25 (stipend figures vary by source) — [GSoC 2026 flyer](https://developers.google.com/open-source/gsoc/resources/downloads/GSoC2026Flyer.pdf), [Canonical deck](https://events.canonical.com/event/154/contributions/1018/attachments/548/885/How%20to%20start%20your%20GSoC%20Journey.pdf)
- Kubernetes Show HN examples and points: see Section 1.
- Artifact Hub already shows security reports per chart version (Section 1) and is a natural integration surface.

### Inferences
- EU 2027 CFP is closing the day of this report; a lightning talk or poster is the lowest-bar entry. For NA 2027 expect a spring 2027 window (inference).
- Sandbox: pursue after there is a public findings page and external adopters; kubescape took 2022 to 2025 to go Sandbox to Incubating.
- A realistic GSoC path for Helmbulance needs the project to be accepted under an umbrella org first; applications for 2027 would be early 2027.

### Gaps
- Not retrieved: KubeWeekly / Learnk8s submission procedures, r/kubernetes self-promotion rules, awesome-list entries, GitHub Marketplace action counts, LFX Mentorship terms, "what Sandbox actually changes" in concrete benefits (only fragments found), CNCF Sandbox queue/acceptance rate.

## 5. Fame metrics and realistic timelines

### Takeaway
Stars for well-known k8s tools: low-thousands (kube-score, kubeconform, kube-linter, polaris, pluto), 5-15k (kind 15.5k, Kubescape 11.8k, kube-bench 8.2k), 30k+ (helm, trivy). Timelines from first release are measured in years.

### Cited Findings
- All numbers are the 2026-10-11 snapshot in Section 1 with creation dates: kube-linter 3,524 stars in 6.1 years, pluto 2,588 in 6.6 years, polaris 3,393 in 7.9 years, kubeconform 3,218 in 6.4 years, kubescape 11,790 in 5.2 years (CNCF-hosted from 2022), trivy 38,337 in 7.5 years — GitHub API
- Kubescape: Sandbox to Incubating in ~2.2 years — [CNCF](https://www.cncf.io/blog/2025/02/26/kubescape-becomes-a-cncf-incubating-project/)
- Coccinelle: ~180 patches within ~1 year of the 2008 release, 400+ by early 2010 — see Section 1.

### Inferences
- Credible 12-month goal for a 2-person team (guess): 300-1,000 stars, 10-30 merged upstream fixes, a few adopters running the Action. Treat as estimate; no sourced benchmark for small-team trajectories found.
- Counts of merged upstream fixes and acknowledged findings are a more defensible fame metric than stars here (OSS-Fuzz and Coccinelle both grew on fix counts).

### Gaps
- Action usage counts (e.g. Marketplace/`dependents`), download counts and star-history curves not gathered.

## 6. What makes a findings page credible

### Takeaway
Credible findings pages show reproducible commands, upstream links, and status (fixed / acknowledged / disputed). Examples found are indirect.

### Cited Findings
- curl's accepted AI-found fixes were credited in commits as "Reported in Joshua's sarif data", giving traceable proof per fix — [Simon Willison](https://simonwillison.net/2025/Oct/2/curl/)
- CodeQL Wall of Fame and GitHub Advisory Database act as the public proof registers for Security Lab findings — [GitHub blog](https://github.blog/security/vulnerability-research/multi-repository-variant-analysis-a-powerful-new-way-to-perform-security-research-across-github/)
- OSS-Fuzz issues become public with reproducer and fix status after the deadline — [OSS-Fuzz guidelines](https://google.github.io/oss-fuzz/getting-started/bug-disclosure-guidelines/)
- GitHub's PVR form itself asks for PoC and impact, indicating the expected content — [GitHub docs](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)

### Inferences
- Suggested per-finding record (design suggestion): chart + version, scenario, one-command repro (kind + helm commands), expected vs actual, upstream issue/PR link, status (reported / acknowledged / fixed / wontfix / disputed), date reported, and for security class the disclosure date. Include a "rejected/disputed" count to show honesty; the UMN and FFmpeg stories show hiding that is what destroys trust.

### Gaps
- No concrete example of a public Helm-chart findings page was found; the closest comparables (OSS-Fuzz tracker, Wall of Fame) are in other domains.
- Datree and Pluto public "hall of fame" equivalents: not found.
