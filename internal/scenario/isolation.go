package scenario

import (
	"fmt"
	"slices"
	"strings"
)

// isolatedSocket is one listening port of one pod of the release, as network-isolation sees it
// (#182): what listens there, which Services send traffic to it, which NetworkPolicies select the
// pod, and whether a pod in a foreign namespace could open a connection to it with the chart's
// policies in place.
type isolatedSocket struct {
	Pod      string
	Workload string   // kind/name of the pod's owner, "" for a bare pod
	Images   []string // the images of the pod's containers
	Port     uint16
	// Services are the Services whose targetPort resolves to this port on this pod.
	Services []string
	// Policies are the NetworkPolicies that select the pod for ingress.
	Policies  []string
	Reachable bool
}

// datastore is a database or broker whose port a chart often bundles, recognised by its image
// and its usual ports. Both must match: an exporter sidecar runs an image named after the engine
// but listens elsewhere, and an app that happens to use 6379 is no Redis.
type datastore struct {
	name   string
	images []string // image names, as imageIs matches them
	ports  []uint16
}

var datastores = []datastore{
	{"PostgreSQL", []string{"postgres", "postgresql", "pgvector", "timescaledb", "postgis"}, []uint16{5432}},
	{"MySQL or MariaDB", []string{"mysql", "mariadb", "percona"}, []uint16{3306, 33060}},
	{"SQL Server", []string{"mssql/server", "mssql-server-linux", "azure-sql-edge"}, []uint16{1433}},
	{"Redis or Valkey", []string{"redis", "valkey", "keydb"}, []uint16{6379, 26379}},
	{"MongoDB", []string{"mongo", "mongodb", "percona-server-mongodb"}, []uint16{27017}},
	{"Elasticsearch or OpenSearch", []string{"elasticsearch", "opensearch"}, []uint16{9200, 9300}},
	{"RabbitMQ", []string{"rabbitmq"}, []uint16{5672, 15672, 25672}},
	{"Memcached", []string{"memcached"}, []uint16{11211}},
	{"Cassandra", []string{"cassandra", "scylla"}, []uint16{9042, 7000}},
	{"etcd", []string{"etcd"}, []uint16{2379, 2380}},
	{"ZooKeeper", []string{"zookeeper", "cp-zookeeper"}, []uint16{2181}},
	// 9093 is the KRaft controller's port in Bitnami's chart, and often the TLS listener.
	{"Kafka", []string{"kafka", "cp-kafka", "cp-server"}, []uint16{9092, 9093}},
	{"ClickHouse", []string{"clickhouse"}, []uint16{8123, 9000}},
}

// datastoreOf names the datastore behind s, or "" when its images and port are not one.
func datastoreOf(s isolatedSocket) string {
	for _, d := range datastores {
		if !slices.Contains(d.ports, s.Port) {
			continue
		}
		for _, image := range s.Images {
			if imageIs(image, d.images) {
				return d.name
			}
		}
	}
	return ""
}

// imageIs reports whether the image's repository name, without registry, path, tag or digest,
// is one of names or starts with one of them followed by a dash: "bitnami/redis:7" and
// "docker.io/library/redis-stack-server" are Redis, "oliver006/redis_exporter" is not. A name with
// a slash is matched against the end of the repository path instead, for an image whose last
// element says nothing alone ("mcr.microsoft.com/mssql/server").
func imageIs(image string, names []string) bool {
	path, _, _ := strings.Cut(strings.ToLower(image), "@")
	if i := strings.LastIndex(path, ":"); i > strings.LastIndex(path, "/") {
		path = path[:i]
	}
	repo := path[strings.LastIndex(path, "/")+1:]
	for _, n := range names {
		if strings.Contains(n, "/") {
			if path == n || strings.HasSuffix(path, "/"+n) {
				return true
			}
			continue
		}
		if repo == n || strings.HasPrefix(repo, n+"-") {
			return true
		}
	}
	return false
}

// isolationFindings turns what network-isolation saw into findings, as the architecture's output
// rule says: only a listener that no Service exposes, or a bundled datastore's port, reachable from
// a foreign namespace is a defect. A reachable Service port of anything else is what Services are
// for, and an unreachable socket is no finding at all.
//
// HB-NI-001: a datastore port reachable from another namespace (high: anyone in the cluster can
// try its credentials, or use it without any when it has none).
// HB-NI-002: a port that no Service exposes reachable from another namespace (medium: an admin,
// debug or metrics listener the chart did not mean to publish).
// A datastore port is reported once, as HB-NI-001, whether or not a Service exposes it.
//
// A datastore is bundled when it sits next to an app: some workload of the release listens on no
// datastore port. A chart that is only the datastore (bitnami/postgresql, a Redis with its
// sentinels) is installed for clients elsewhere, so its Service port reachable from another
// namespace is what it is for: a note, not HB-NI-001. Its other ports are judged as any other
// (review of #186).
func isolationFindings(sockets []isolatedSocket) (findings []Finding, notes []string) {
	bundled := false
	for _, s := range sockets {
		if datastoreOf(s) == "" && !workloadServes(sockets, s) {
			bundled = true
			break
		}
	}
	var datastoreEv, unexposedEv []string
	for _, s := range sockets {
		if !s.Reachable {
			continue
		}
		where := fmt.Sprintf("`%s` port %d", s.Pod, s.Port)
		if s.Workload != "" {
			where = fmt.Sprintf("`%s` (pod `%s`) port %d", s.Workload, s.Pod, s.Port)
		}
		policy := "no NetworkPolicy selects the pod"
		if len(s.Policies) > 0 {
			policy = "the NetworkPolicies that select the pod (" + strings.Join(s.Policies, ", ") + ") allow it"
		}
		if name := datastoreOf(s); name != "" {
			if bundled {
				exposed := "no Service exposes it"
				if len(s.Services) > 0 {
					exposed = "Service " + strings.Join(s.Services, ", ")
				}
				datastoreEv = append(datastoreEv, fmt.Sprintf("%s: %s, reachable from another namespace; %s; %s.", where, name, exposed, policy))
				continue
			}
			if len(s.Services) > 0 {
				notes = append(notes, fmt.Sprintf("%s: %s, reachable from another namespace through Service %s; the chart is the datastore, so that is how its clients connect",
					where, name, strings.Join(s.Services, ", ")))
				continue
			}
		}
		if len(s.Services) == 0 {
			unexposedEv = append(unexposedEv, fmt.Sprintf("%s: reachable from another namespace, though no Service exposes it; %s.", where, policy))
		}
	}
	if len(datastoreEv) > 0 {
		findings = append(findings, Finding{
			ID:       "HB-NI-001",
			Title:    "A bundled datastore is reachable from other namespaces",
			Severity: "high",
			Evidence: datastoreEv,
			Impact: "Any pod in the cluster, in any namespace, can connect to the database or broker the chart " +
				"bundles and try its credentials, or use it freely when it has none.",
			Fix: "Ship a NetworkPolicy that selects the datastore's pods and allows ingress only from the " +
				"chart's own pods (a podSelector on their labels), and enable it by default.",
		})
	}
	if len(unexposedEv) > 0 {
		findings = append(findings, Finding{
			ID:       "HB-NI-002",
			Title:    "A port that no Service exposes is reachable from other namespaces",
			Severity: "medium",
			Evidence: unexposedEv,
			Impact: "A listener the chart did not publish, often an admin, debug or metrics endpoint, is open " +
				"to every pod in the cluster.",
			Fix: "Bind the listener to localhost if only the pod uses it, or add a NetworkPolicy that allows " +
				"ingress to the pod only on the ports its Services expose.",
		})
	}
	return findings, notes
}

// workloadServes reports whether s belongs to a workload (or bare pod) that also listens on a
// datastore port, such as a database's exporter sidecar or admin port: the workload is a
// datastore, whatever its other ports are.
func workloadServes(sockets []isolatedSocket, s isolatedSocket) bool {
	owner := func(x isolatedSocket) string {
		if x.Workload != "" {
			return x.Workload
		}
		return "pod/" + x.Pod
	}
	for _, o := range sockets {
		if owner(o) == owner(s) && datastoreOf(o) != "" {
			return true
		}
	}
	return false
}
