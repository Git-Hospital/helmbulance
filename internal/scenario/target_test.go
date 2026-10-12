package scenario

import (
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
		got, err := pickTarget(c.services)
		if err != nil || got != c.want {
			t.Errorf("%s: pickTarget = %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestPickTargetNoHTTPPort(t *testing.T) {
	if got, err := pickTarget([]kube.Service{svc("db", kube.ServicePort{Name: "pg", Port: 5432})}); err == nil {
		t.Errorf("pickTarget = %q, want an error", got)
	}
}
