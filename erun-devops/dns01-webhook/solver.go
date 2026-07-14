// Command dns01-webhook is a cert-manager ACME DNS-01 webhook that forwards
// present/cleanup to the erun DNS-01 broker over an authenticated transport. It
// holds no DNS credential itself: it reads the challenge's env-scoped token from
// a Secret and presents it to the broker, which authorizes the write against
// that env's subzone. One shim per cluster serves every tenant's per-tenant
// Issuers, so tenant isolation is enforced centrally at the broker, not here.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// solverName is the cert-manager webhook solverName a per-tenant Issuer selects.
const solverName = "powerdns-broker"

// brokerSolver implements webhook.Solver by forwarding to the erun DNS-01 broker.
type brokerSolver struct {
	kube kubernetes.Interface
	http *http.Client
}

// solverConfig is the per-Issuer `config` block: where the broker is and which
// Secret in the challenge's namespace holds this env's DNS-01 token.
type solverConfig struct {
	BrokerURL      string `json:"brokerURL"`
	TokenSecretRef struct {
		Name string `json:"name"`
		Key  string `json:"key"`
	} `json:"tokenSecretRef"`
}

func (s *brokerSolver) Name() string { return solverName }

func (s *brokerSolver) Initialize(cfg *rest.Config, _ <-chan struct{}) error {
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	s.kube = client
	s.http = &http.Client{Timeout: 30 * time.Second}
	return nil
}

func (s *brokerSolver) Present(ch *v1alpha1.ChallengeRequest) error {
	return s.forward(ch, "present")
}

func (s *brokerSolver) CleanUp(ch *v1alpha1.ChallengeRequest) error {
	return s.forward(ch, "cleanup")
}

func (s *brokerSolver) forward(ch *v1alpha1.ChallengeRequest, action string) error {
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return err
	}
	token, err := s.readToken(ch.ResourceNamespace, cfg)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]string{"fqdn": ch.ResolvedFQDN, "value": ch.Key})
	if err != nil {
		return err
	}
	url := strings.TrimRight(cfg.BrokerURL, "/") + "/" + action
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("dns01 broker %s: %w", action, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("dns01 broker %s rejected the challenge: %s", action, resp.Status)
	}
	return nil
}

func (s *brokerSolver) readToken(namespace string, cfg solverConfig) (string, error) {
	secret, err := s.kube.CoreV1().Secrets(namespace).Get(context.Background(), cfg.TokenSecretRef.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("read dns01 token secret %s/%s: %w", namespace, cfg.TokenSecretRef.Name, err)
	}
	key := cfg.TokenSecretRef.Key
	if key == "" {
		key = "token"
	}
	data, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("dns01 token secret %s has no key %q", cfg.TokenSecretRef.Name, key)
	}
	return strings.TrimSpace(string(data)), nil
}

func loadConfig(raw *extapi.JSON) (solverConfig, error) {
	var cfg solverConfig
	if raw == nil {
		return cfg, fmt.Errorf("dns01 webhook: no solver config provided")
	}
	if err := json.Unmarshal(raw.Raw, &cfg); err != nil {
		return cfg, fmt.Errorf("dns01 webhook: invalid solver config: %w", err)
	}
	if cfg.BrokerURL == "" || cfg.TokenSecretRef.Name == "" {
		return cfg, fmt.Errorf("dns01 webhook: solver config requires brokerURL and tokenSecretRef.name")
	}
	return cfg, nil
}
