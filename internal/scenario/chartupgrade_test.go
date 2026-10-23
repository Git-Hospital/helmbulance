package scenario

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egezamb/helmbulance/internal/execx"
	"github.com/egezamb/helmbulance/internal/helm"
	"github.com/egezamb/helmbulance/internal/kube"
	"github.com/egezamb/helmbulance/internal/oracle"
)

// runChartUpgrade runs chart-upgrade against a fake kubectl that sees an empty namespace and a
// fake helm whose upgrade to the chart under test (the only call without --version) prints
// upgradeErr and fails.
func runChartUpgrade(t *testing.T, upgradeErr string) Result {
	t.Helper()
	bin := t.TempDir()
	helmScript := `#!/bin/sh
case "$*" in
  *"get metadata"*) echo '{"version":"1.0.0"}'; exit 0 ;;
  upgrade*--version*|install*|uninstall*) exit 0 ;;
  upgrade*) echo "` + upgradeErr + `" >&2; exit 1 ;;
esac
exit 0
`
	for name, script := range map[string]string{"helm": helmScript, "kubectl": "#!/bin/sh\necho '{\"items\":[]}'\n"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	hasher, err := oracle.NewHasher()
	if err != nil {
		t.Fatal(err)
	}
	runner := &execx.Runner{}
	env := &Env{
		Runner:      runner,
		Helm:        &helm.Client{Runner: runner, Timeout: time.Minute},
		Kube:        &kube.Client{Runner: runner},
		Hasher:      hasher,
		Chart:       "./v2",
		From:        "repo/chart",
		FromVersion: "1.0.0",
		Log:         io.Discard,
	}
	return ChartUpgrade{}.Run(t.Context(), env)
}

func TestChartUpgradeTimeoutIsInconclusive(t *testing.T) {
	res := runChartUpgrade(t, "Error: UPGRADE FAILED: resource Deployment/ns/web not ready. status: InProgress\ncontext deadline exceeded")
	if res.Verdict != Inconclusive || len(res.Findings) > 0 {
		t.Errorf("verdict = %s with %d finding(s) (%s), want INCONCLUSIVE and none", res.Verdict, len(res.Findings), res.Summary)
	}
}

func TestChartUpgradeRejectionIsAFinding(t *testing.T) {
	res := runChartUpgrade(t, "Error: UPGRADE FAILED: cannot patch db with kind StatefulSet: spec: Forbidden: field is immutable")
	if res.Verdict != Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-CU-001" {
		t.Errorf("verdict = %s (%s), want FAIL with HB-CU-001", res.Verdict, res.Summary)
	}
}
