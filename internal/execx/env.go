package execx

import "strings"

// envAllowed lists the variables helm, kind and kubectl may inherit from the caller. Everything
// else is dropped: helm reads HELM_KUBEAPISERVER, HELM_KUBETOKEN, HELM_KUBECONTEXT, HELM_DRIVER
// and similar variables, and with them a run could install into a real cluster while kubectl
// reads the sandbox.
var envAllowed = map[string]bool{
	// The basics every command needs.
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "TMPDIR": true, "LANG": true, "TZ": true,
	"TERM": true, "NO_COLOR": true,
	// Where helm keeps its cache, config, data and plugins; none of them point at a cluster. The
	// caller's repository and registry credentials and helm plugins are inherited on purpose, so
	// private charts can be pulled.
	"HELM_CACHE_HOME": true, "HELM_CONFIG_HOME": true, "HELM_DATA_HOME": true,
	"HELM_REPOSITORY_CACHE": true, "HELM_REPOSITORY_CONFIG": true, "HELM_REGISTRY_CONFIG": true,
	"HELM_PLUGINS":   true,
	"XDG_CACHE_HOME": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_STATE_HOME": true,
	"XDG_RUNTIME_DIR": true,
	// Proxies and CA bundles, for pulling charts and images behind a corporate proxy.
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "ALL_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true, "all_proxy": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	// kind with Docker contexts, Colima or Podman (rootless Podman keeps its socket under
	// XDG_RUNTIME_DIR).
	"DOCKER_HOST": true, "DOCKER_CONTEXT": true, "DOCKER_CONFIG": true, "DOCKER_API_VERSION": true,
	"DOCKER_CERT_PATH": true, "DOCKER_TLS_VERIFY": true, "CONTAINER_HOST": true,
	"KIND_EXPERIMENTAL_PROVIDER": true, "KIND_EXPERIMENTAL_DOCKER_NETWORK": true,
}

// SandboxEnv returns the environment for commands that act on a sandbox cluster: the allowed
// variables from environ, and HELM_DRIVER=secret so releases are always stored in the sandbox.
func SandboxEnv(environ []string) []string {
	var env []string
	for _, kv := range environ {
		key, _, ok := strings.Cut(kv, "=")
		if ok && envAllowed[key] || strings.HasPrefix(key, "LC_") {
			env = append(env, kv)
		}
	}
	return append(env, "HELM_DRIVER=secret")
}

