package scenario

import (
	"strings"
	"testing"

	"github.com/Git-Hospital/helmbulance/internal/kube"
)

func svc(name string, ports ...kube.ServicePort) kube.Service {
	return kube.Service{Name: name, Type: "ClusterIP", ClusterIP: "10.96.0.10", Selector: map[string]string{"app": name}, Ports: ports}
}

func TestPickTarget(t *testing.T) {
	cases := []struct {
		name     string
		services []kube.Service
		want     string
	}{
		{"named http beats a common number", []kube.Service{
			svc("metrics", kube.ServicePort{Name: "metrics", Port: 8080}),
			svc("web", kube.ServicePort{Name: "http", Port: 9000}),
		}, "web:9000"},
		{"a common number beats http in the name", []kube.Service{
			svc("admin", kube.ServicePort{Name: "http-admin", Port: 9001}),
			svc("app", kube.ServicePort{Name: "main", Port: 3000}),
		}, "app:3000"},
		{"UDP is never picked", []kube.Service{
			svc("dns", kube.ServicePort{Name: "http", Port: 80, Protocol: "UDP"}),
			svc("app", kube.ServicePort{Name: "http-alt", Port: 9000}),
		}, "app:9000"},
		// The requests are plain http://, so TLS and gRPC ports are never picked (#57).
		{"https by name is not plain HTTP", []kube.Service{
			svc("tls", kube.ServicePort{Name: "https", Port: 8080}),
			svc("app", kube.ServicePort{Name: "http-alt", Port: 9000}),
		}, "app:9000"},
		{"gRPC on a common number is not HTTP", []kube.Service{
			svc("api", kube.ServicePort{Name: "grpc-api", Port: 8000}),
			svc("app", kube.ServicePort{Name: "http-alt", Port: 9000}),
		}, "app:9000"},
		{"appProtocol decides over the name", []kube.Service{
			svc("api", kube.ServicePort{Name: "http", Port: 80, AppProtocol: "kubernetes.io/h2c"}),
			svc("app", kube.ServicePort{Name: "web", Port: 9000, AppProtocol: "http"}),
		}, "app:9000"},
		{"443 is TLS whatever its name", []kube.Service{
			svc("edge", kube.ServicePort{Name: "web", Port: 443}),
			svc("app", kube.ServicePort{Name: "http-alt", Port: 9000}),
		}, "app:9000"},
		{"oauth2 in a name is not h2", []kube.Service{
			svc("auth", kube.ServicePort{Name: "oauth2", Port: 8080}),
		}, "auth:8080"},
		// A metrics port often answers only /metrics, so the control fails (#162).
		{"a metrics Service on a common number loses to any other", []kube.Service{
			svc("kyverno-svc-metrics", kube.ServicePort{Name: "metrics-port", Port: 8000}),
			svc("kyverno-ui", kube.ServicePort{Name: "http-alt", Port: 9000}),
		}, "kyverno-ui:9000"},
		{"a port named metrics loses even when it is called http", []kube.Service{
			svc("app", kube.ServicePort{Name: "http-metrics", Port: 80}, kube.ServicePort{Name: "http-api", Port: 9000}),
		}, "app:9000"},
		{"a metrics port is still picked when it is the only one", []kube.Service{
			svc("exporter-metrics", kube.ServicePort{Name: "http", Port: 9100}),
		}, "exporter-metrics:9100"},
		{"among metrics ports the usual order still holds", []kube.Service{
			svc("monitoring-exporter", kube.ServicePort{Name: "tcp", Port: 8080}),
			svc("monitoring-ui", kube.ServicePort{Name: "http", Port: 3000}),
		}, "monitoring-ui:3000"},
		{"metricsserver is not the word metrics", []kube.Service{
			svc("metricsserver", kube.ServicePort{Name: "http", Port: 80}),
			svc("app", kube.ServicePort{Name: "http-alt", Port: 9000}),
		}, "metricsserver:80"},
		{"a tie keeps the first", []kube.Service{
			svc("a", kube.ServicePort{Name: "web", Port: 1}),
			svc("b", kube.ServicePort{Name: "http", Port: 2}),
		}, "a:1"},
	}
	for _, c := range cases {
		got, err := pickTarget(c.services, nil)
		if err != nil || got != c.want {
			t.Errorf("%s: pickTarget = %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestPickTargetNoHTTPPort(t *testing.T) {
	if got, err := pickTarget([]kube.Service{svc("db", kube.ServicePort{Name: "pg", Port: 5432})}, nil); err == nil {
		t.Errorf("pickTarget = %q, want an error", got)
	}
}

func TestPickTargetSkipsServicesThatLeaveThePods(t *testing.T) {
	ext := svc("ext", kube.ServicePort{Name: "http", Port: 80})
	ext.Type, ext.ClusterIP = "ExternalName", ""
	headless := svc("headless", kube.ServicePort{Name: "http", Port: 80})
	headless.ClusterIP = "None"
	public := svc("public", kube.ServicePort{Name: "http", Port: 80})
	public.ExternalIPs = []string{"203.0.113.5"}
	app := svc("app", kube.ServicePort{Name: "main", Port: 8080})

	got, err := pickTarget([]kube.Service{ext, headless, public, app}, nil)
	if err != nil || got != "app:8080" {
		t.Errorf("pickTarget = %q, %v; want app:8080", got, err)
	}
	_, err = pickTarget([]kube.Service{ext, headless, public}, nil)
	if err == nil || !strings.Contains(err.Error(), "skipped ext (ExternalName), headless (headless), public (externalIPs)") {
		t.Errorf("err = %v, want the skipped Services named", err)
	}
}

func TestPickTargetSkipsServicesWithEndpointsOutsideThePods(t *testing.T) {
	web := svc("web", kube.ServicePort{Name: "http", Port: 80})
	app := svc("app", kube.ServicePort{Name: "main", Port: 8080})
	outside := map[string][]string{"web": {"203.0.113.9"}}

	got, err := pickTarget([]kube.Service{web, app}, outside)
	if err != nil || got != "app:8080" {
		t.Errorf("pickTarget = %q, %v; want app:8080", got, err)
	}
	_, err = pickTarget([]kube.Service{web}, outside)
	if err == nil || !strings.Contains(err.Error(), "skipped web (endpoints that are not pods in the namespace: 203.0.113.9)") {
		t.Errorf("err = %v, want web named with its outside address", err)
	}
}
