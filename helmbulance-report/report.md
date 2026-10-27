# Helmbulance report: PASS

- Chart: `/private/tmp/claude-501/-Users-egezambelli/9262060b-56fd-4f40-9398-6d4639019983/scratchpad/swfs-up/k8s/charts/seaweedfs`
- Helm: v4.3.0+gbec5b06
- Sandbox: kind cluster `helmbulance-3d5ac616`
- Duration: 8m44s

| Scenario | Verdict | Summary |
|---|---|---|
| `upgrade-without-values` | PASS | the release renders no Secrets; Secret drift was not tested |
| `zero-downtime` | SKIPPED | no Service to send traffic to: no Service port looks like HTTP; give --traffic-target service:port (skipped hb-zd-seaweedfs-filer (headless), hb-zd-seaweedfs-filer-client (headless), hb-zd-seaweedfs-master (headless), hb-zd-seaweedfs-volume (headless)) |
| `reinstall` | PASS | Secrets came back unchanged after uninstall and install; the release has no volumes |
| `gitops-render` | SKIPPED | the release has no Secrets, so there is nothing a client-side render could change |
