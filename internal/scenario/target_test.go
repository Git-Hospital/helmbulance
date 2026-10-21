package scenario

import (
	"strings"
	"testing"

	"github.com/egezamb/helmbulance/internal/kube"
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
