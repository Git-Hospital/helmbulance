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
	got, notes := isolationFindings(sockets)
	if len(notes) != 0 {
		t.Errorf("notes %q, want none: the Redis sits next to an app", notes)
	}
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
	got, _ := isolationFindings([]isolatedSocket{
		{Pod: "mongo-0", Images: []string{"mongo:8"}, Port: 27017, Reachable: true},
		{Pod: "web", Images: []string{"example/app"}, Port: 8080, Services: []string{"web"}, Reachable: true},
	})
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
		if got, _ := isolationFindings(sockets); len(got) != 0 {
			t.Errorf("%s: findings %v, want none", name, got)
		}
	}
}

// A chart that is only the datastore is installed for clients in other namespaces: its Service
// port is a note, not HB-NI-001. A port no Service exposes is still HB-NI-002 (review of #186).
func TestIsolationFindingsDatastoreChart(t *testing.T) {
	pg := []string{"bitnami/postgresql:17", "bitnami/postgres-exporter:0.17"}
	sockets := []isolatedSocket{
		{Pod: "pg-primary-0", Workload: "StatefulSet/pg-primary", Images: pg, Port: 5432, Services: []string{"pg"}, Reachable: true},
		// The exporter sidecar of the same pod is part of the datastore, not an app next to it.
		{Pod: "pg-primary-0", Workload: "StatefulSet/pg-primary", Images: pg, Port: 9187, Services: []string{"pg-metrics"}, Reachable: true},
		{Pod: "pg-read-0", Workload: "StatefulSet/pg-read", Images: pg, Port: 5432, Services: []string{"pg-read"}, Reachable: true},
		{Pod: "pg-read-0", Workload: "StatefulSet/pg-read", Images: pg, Port: 8008, Reachable: true},
	}
	got, notes := isolationFindings(sockets)
	if len(got) != 1 || got[0].ID != "HB-NI-002" || !strings.Contains(got[0].Evidence[0], "port 8008") {
		t.Errorf("findings %v, want only HB-NI-002 for port 8008", got)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "`StatefulSet/pg-primary` (pod `pg-primary-0`) port 5432: PostgreSQL, reachable from another namespace through Service pg; the chart is the datastore") {
		t.Errorf("notes %q, want one per datastore Service port", notes)
	}
}

// Images Arda tried on #186 that were not recognised.
func TestDatastoreOf(t *testing.T) {
	for _, c := range []struct {
		image string
		port  uint16
		want  string
	}{
		{"confluentinc/cp-kafka:7.9.0", 9092, "Kafka"},
		{"confluentinc/cp-server:7.9.0", 9092, "Kafka"},
		{"confluentinc/cp-zookeeper:7.9.0", 2181, "ZooKeeper"},
		{"percona/percona-server-mongodb:8.0", 27017, "MongoDB"},
		{"percona/percona-server:8.4", 3306, "MySQL or MariaDB"},
		{"bitnami/kafka:4.0", 9093, "Kafka"},
		{"mcr.microsoft.com/mssql/server:2022-latest", 1433, "SQL Server"},
		{"mcr.microsoft.com/azure-sql-edge", 1433, "SQL Server"},
		{"example/server:1", 1433, ""},        // a last element of "server" alone is nothing
		{"example/mssql/server-ui", 1433, ""}, // nor a longer name under the same path
		{"confluentinc/cp-kafka-rest:7.9.0", 8082, ""},
	} {
		if got := datastoreOf(isolatedSocket{Images: []string{c.image}, Port: c.port}); got != c.want {
			t.Errorf("datastoreOf(%s, %d) = %q, want %q", c.image, c.port, got, c.want)
		}
	}
}
