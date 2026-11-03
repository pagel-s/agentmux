package sandbox

import "strings"

const workflowCertificateTarget = "/run/agentmux/egress-ca.pem"

// Each supported client receives the same per-turn public root. This replaces
// ambient trust paths without granting any direct network route. Clients which
// pin a server certificate or ignore proxy/trust settings fail closed.
func workflowCertificateEnvironment(input []string) []string {
	names := map[string]bool{}
	for _, name := range []string{"CODEX_CA_CERTIFICATE", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "NODE_EXTRA_CA_CERTS", "GIT_SSL_CAINFO", "AWS_CA_BUNDLE"} {
		names[name] = true
	}
	names["NODE_USE_ENV_PROXY"] = true
	result := make([]string, 0, len(input)+len(names))
	for _, entry := range input {
		name, _, _ := strings.Cut(entry, "=")
		if !names[name] {
			result = append(result, entry)
		}
	}
	for _, name := range []string{"CODEX_CA_CERTIFICATE", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "NODE_EXTRA_CA_CERTS", "GIT_SSL_CAINFO", "AWS_CA_BUNDLE"} {
		result = append(result, name+"="+workflowCertificateTarget)
	}
	// Modern Node fetch otherwise ignores the relay's HTTP(S)_PROXY variables.
	// The namespace still denies direct networking for incompatible runtimes.
	result = append(result, "NODE_USE_ENV_PROXY=1")
	return result
}
