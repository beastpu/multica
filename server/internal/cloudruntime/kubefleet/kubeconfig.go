package kubefleet

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// kubeClient carries everything needed to talk to a cluster's API server:
// the base URL, an HTTP client with the right TLS trust (and optional client
// cert), and a static bearer token (empty for cert-based auth).
type kubeClient struct {
	apiURL      string
	http        *http.Client
	bearerToken string // static; when set, used instead of reading TokenFile
}

// kubeconfigFile is the minimal subset of a kubeconfig we parse. We resolve
// current-context → its cluster + user and support the two common auth modes:
// a bearer token, or a client certificate.
type kubeconfigFile struct {
	Clusters []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server string `yaml:"server"`
			CAData string `yaml:"certificate-authority-data"`
			CAFile string `yaml:"certificate-authority"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			Token    string `yaml:"token"`
			CertData string `yaml:"client-certificate-data"`
			KeyData  string `yaml:"client-key-data"`
		} `yaml:"user"`
	} `yaml:"users"`
	Contexts []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster string `yaml:"cluster"`
			User    string `yaml:"user"`
		} `yaml:"context"`
	} `yaml:"contexts"`
	CurrentContext string `yaml:"current-context"`
}

// loadKubeconfig reads a kubeconfig from a file path or inline YAML content and
// builds a kubeClient for its current-context. Used when
// MULTICA_CLOUD_RUNTIME_KUBECONFIG points at a remote cluster; unset falls back
// to in-cluster.
func loadKubeconfig(pathOrContent string) (*kubeClient, error) {
	data := []byte(pathOrContent)
	// A short, single-line value with no YAML markers is treated as a path.
	if !strings.Contains(pathOrContent, "\n") && !strings.Contains(pathOrContent, "apiVersion") {
		raw, err := os.ReadFile(strings.TrimSpace(pathOrContent))
		if err != nil {
			return nil, fmt.Errorf("kubefleet: read kubeconfig %q: %w", pathOrContent, err)
		}
		data = raw
	}

	var kc kubeconfigFile
	if err := yaml.Unmarshal(data, &kc); err != nil {
		return nil, fmt.Errorf("kubefleet: parse kubeconfig: %w", err)
	}
	if kc.CurrentContext == "" {
		return nil, fmt.Errorf("kubefleet: kubeconfig has no current-context")
	}

	var clusterName, userName string
	for _, c := range kc.Contexts {
		if c.Name == kc.CurrentContext {
			clusterName, userName = c.Context.Cluster, c.Context.User
			break
		}
	}
	if clusterName == "" {
		return nil, fmt.Errorf("kubefleet: current-context %q not found", kc.CurrentContext)
	}

	var server, caData, caFile string
	for _, c := range kc.Clusters {
		if c.Name == clusterName {
			server, caData, caFile = c.Cluster.Server, c.Cluster.CAData, c.Cluster.CAFile
			break
		}
	}
	if server == "" {
		return nil, fmt.Errorf("kubefleet: cluster %q has no server", clusterName)
	}

	tlsCfg := &tls.Config{}
	if caData != "" {
		pem, err := base64.StdEncoding.DecodeString(caData)
		if err != nil {
			return nil, fmt.Errorf("kubefleet: decode certificate-authority-data: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("kubefleet: certificate-authority-data has no certificates")
		}
		tlsCfg.RootCAs = pool
	} else if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("kubefleet: read certificate-authority %q: %w", caFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("kubefleet: certificate-authority %q has no certificates", caFile)
		}
		tlsCfg.RootCAs = pool
	}

	var token string
	for _, u := range kc.Users {
		if u.Name != userName {
			continue
		}
		token = u.User.Token
		if u.User.CertData != "" && u.User.KeyData != "" {
			certPEM, err := base64.StdEncoding.DecodeString(u.User.CertData)
			if err != nil {
				return nil, fmt.Errorf("kubefleet: decode client-certificate-data: %w", err)
			}
			keyPEM, err := base64.StdEncoding.DecodeString(u.User.KeyData)
			if err != nil {
				return nil, fmt.Errorf("kubefleet: decode client-key-data: %w", err)
			}
			cert, err := tls.X509KeyPair(certPEM, keyPEM)
			if err != nil {
				return nil, fmt.Errorf("kubefleet: build client cert: %w", err)
			}
			tlsCfg.Certificates = []tls.Certificate{cert}
		}
		break
	}
	if token == "" && len(tlsCfg.Certificates) == 0 {
		return nil, fmt.Errorf("kubefleet: user %q has neither token nor client certificate", userName)
	}

	return &kubeClient{
		apiURL: strings.TrimRight(server, "/"),
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
		bearerToken: token,
	}, nil
}
