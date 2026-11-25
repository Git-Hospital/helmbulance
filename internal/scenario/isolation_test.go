package scenario

import (
	"reflect"
	"strings"
	"testing"
)

func TestImageIs(t *testing.T) {
	redis := []string{"redis", "valkey"}
	for image, want := range map[string]bool{
		"redis":     true,
		"redis:7.4": true,
		"docker.io/bitnami/redis:7.4.1-debian-12":          true,
		"registry.example.com:5000/team/valkey@sha256:abc": true,
		"redis/redis-stack-server:7.2":                     true,
		"REDIS":                                            true,
		"oliver006/redis_exporter:v1.62":                   false,
		"redisinsight":                                     false,
		"myapp:redis":                                      false,
		"redis-proxy/app":                                  false,
	} {
		if got := imageIs(image, redis); got != want {
			t.Errorf("imageIs(%q) = %v, want %v", image, got, want)
		}
	}
}

func TestIsolationFindings(t *testing.T) {
	sockets := []isolatedSocket{
		// A bundled Redis behind its Service, no policy: HB-NI-001.
		{Pod: "app-redis-0", Workload: "StatefulSet/app-redis", Images: []string{"bitnami/redis:7.4"}, Port: 6379,
			Services: []string{"app-redis"}, Reachable: true},
		// The same pod's exporter sidecar on 9121, exposed by a metrics Service: a normal Service port.
		{Pod: "app-redis-0", Workload: "StatefulSet/app-redis", Images: []string{"bitnami/redis:7.4", "oliver006/redis_exporter"},
			Port: 9121, Services: []string{"app-redis-metrics"}, Reachable: true},
		// The app's admin port, behind no Service, allowed by a policy: HB-NI-002.
		{Pod: "app-web-1", Workload: "Deployment/app-web", Images: []string{"example/app"}, Port: 9090,
			Policies: []string{"app-web"}, Reachable: true},
		// The app's Service port: no finding.
		{Pod: "app-web-1", Workload: "Deployment/app-web", Images: []string{"example/app"}, Port: 8080,
			Services: []string{"app-web"}, Reachable: true},
		// A Postgres a policy blocks: no finding.
		{Pod: "app-db-0", Workload: "StatefulSet/app-db", Images: []string{"postgres:17"}, Port: 5432, Policies: []string{"app-db"}},
		// An unexposed port that is blocked: no finding.
		{Pod: "app-web-1", Workload: "Deployment/app-web", Images: []string{"example/app"}, Port: 6060},
		// An app that happens to listen on 6379 is no Redis: an unexposed port, HB-NI-002.
		{Pod: "tool", Images: []string{"example/tool"}, Port: 6379, Reachable: true},
	}
	got := isolationFindings(sockets)
	if len(got) != 2 || got[0].ID != "HB-NI-001" || got[1].ID != "HB-NI-002" {
		t.Fatalf("findings %v, want HB-NI-001 then HB-NI-002", got)
	}
	if want := []string{"`StatefulSet/app-redis` (pod `app-redis-0`) port 6379: Redis or Valkey, reachable from another namespace; Service app-redis; no NetworkPolicy selects the pod."}; !reflect.DeepEqual(got[0].Evidence, want) {
		t.Errorf("HB-NI-001 evidence %q, want %q", got[0].Evidence, want)
	}
	if got[0].Severity != "high" || got[1].Severity != "medium" {
		t.Errorf("severities %s and %s, want high and medium", got[0].Severity, got[1].Severity)
	}
	want := []string{
		"`Deployment/app-web` (pod `app-web-1`) port 9090: reachable from another namespace, though no Service exposes it; the NetworkPolicies that select the pod (app-web) allow it.",
		"`tool` port 6379: reachable from another namespace, though no Service exposes it; no NetworkPolicy selects the pod.",
	}
	if !reflect.DeepEqual(got[1].Evidence, want) {
		t.Errorf("HB-NI-002 evidence %q, want %q", got[1].Evidence, want)
	}
	for _, f := range got {
		if f.Title == "" || f.Impact == "" || f.Fix == "" {
			t.Errorf("%s lacks a title, impact or fix", f.ID)
		}
	}
}

// A datastore behind no Service is reported once, as a datastore, and says no Service exposes it.
func TestIsolationFindingsUnexposedDatastore(t *testing.T) {
	got := isolationFindings([]isolatedSocket{{Pod: "mongo-0", Images: []string{"mongo:8"}, Port: 27017, Reachable: true}})
	if len(got) != 1 || got[0].ID != "HB-NI-001" || !strings.Contains(got[0].Evidence[0], "MongoDB, reachable from another namespace; no Service exposes it;") {
		t.Errorf("findings %v, want one HB-NI-001 that says no Service exposes it", got)
	}
}

func TestIsolationFindingsNone(t *testing.T) {
	for name, sockets := range map[string][]isolatedSocket{
		"nothing":            nil,
		"all blocked":        {{Pod: "db-0", Images: []string{"postgres"}, Port: 5432}, {Pod: "web", Port: 9090}},
		"only Service ports": {{Pod: "web", Images: []string{"nginx"}, Port: 80, Services: []string{"web"}, Reachable: true}},
	} {
		if got := isolationFindings(sockets); len(got) != 0 {
			t.Errorf("%s: findings %v, want none", name, got)
		}
	}
}
