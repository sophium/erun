package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/oidcbootstrap"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

// oidcBootstrapCommand is the subcommand the erun-zitadel chart runs: as the
// oidc-bootstrap sidecar, and with --restore-pats as the restore-pats init
// container.
const oidcBootstrapCommand = "oidc-bootstrap"

// patPollInterval is how often the sidecar looks for the org-owner PAT core
// writes at first-instance init.
const patPollInterval = 2 * time.Second

type oidcBootstrapConfig struct {
	CorePort            int
	ExternalDomain      string
	ProjectName         string
	ConfigMapName       string
	PATsSecretName      string
	Namespace           string
	BootstrapDir        string
	ConsoleRedirectURIs string
	MobileRedirectURIs  string
	Interval            time.Duration
	Once                bool
	RestorePATs         bool
}

// resolveOIDCBootstrapConfig layers flags over the environment the chart sets.
func resolveOIDCBootstrapConfig(args []string) (oidcBootstrapConfig, error) {
	cfg := oidcBootstrapConfig{
		CorePort:            intEnvOrDefault("CORE_PORT", 8080),
		ExternalDomain:      strings.TrimSpace(os.Getenv("EXTERNAL_DOMAIN")),
		ProjectName:         strings.TrimSpace(os.Getenv("PROJECT_NAME")),
		ConfigMapName:       strings.TrimSpace(os.Getenv("CONFIGMAP_NAME")),
		PATsSecretName:      strings.TrimSpace(os.Getenv("PATS_SECRET_NAME")),
		Namespace:           strings.TrimSpace(os.Getenv("POD_NAMESPACE")),
		BootstrapDir:        envOrDefault("BOOTSTRAP_DIR", "/zitadel/bootstrap"),
		ConsoleRedirectURIs: envOrDefault("CONSOLE_REDIRECT_URIS", "[]"),
		MobileRedirectURIs:  envOrDefault("MOBILE_REDIRECT_URIS", "[]"),
		Interval:            5 * time.Minute,
	}
	flags := flag.NewFlagSet("eapi "+oidcBootstrapCommand, flag.ContinueOnError)
	flags.Usage = func() {
		_, _ = fmt.Fprintf(flags.Output(), "Usage: eapi %s [flags]\n\n"+
			"Reconciles the platform's OIDC applications (erun-console, erun-cli, erun-mobile)\n"+
			"against the Zitadel core in this pod, publishes their client ids to a ConfigMap and\n"+
			"persists the bootstrap PATs to a Secret. Runs as the erun-zitadel chart's sidecar.\n"+
			"With --restore-pats it instead seeds the bootstrap directory from that Secret and exits.\n\n", oidcBootstrapCommand)
		flags.PrintDefaults()
	}
	flags.IntVar(&cfg.CorePort, "core-port", cfg.CorePort, "Loopback port of the Zitadel core Management API (env CORE_PORT)")
	flags.StringVar(&cfg.ExternalDomain, "external-domain", cfg.ExternalDomain, "Host Zitadel resolves the instance from; sent as every call's Host header (env EXTERNAL_DOMAIN)")
	flags.StringVar(&cfg.ProjectName, "project-name", cfg.ProjectName, "Zitadel project holding the OIDC applications (env PROJECT_NAME)")
	flags.StringVar(&cfg.ConfigMapName, "configmap-name", cfg.ConfigMapName, "ConfigMap the client ids are published to (env CONFIGMAP_NAME)")
	flags.StringVar(&cfg.PATsSecretName, "pats-secret-name", cfg.PATsSecretName, "Secret the bootstrap PATs are persisted to and restored from (env PATS_SECRET_NAME)")
	flags.StringVar(&cfg.Namespace, "namespace", cfg.Namespace, "Namespace of the ConfigMap and the Secret (env POD_NAMESPACE)")
	flags.StringVar(&cfg.BootstrapDir, "bootstrap-dir", cfg.BootstrapDir, "Directory core writes the bootstrap PATs into (env BOOTSTRAP_DIR)")
	flags.StringVar(&cfg.ConsoleRedirectURIs, "console-redirect-uris", cfg.ConsoleRedirectURIs, "JSON array of erun-console redirect URIs; empty creates no console app (env CONSOLE_REDIRECT_URIS)")
	flags.StringVar(&cfg.MobileRedirectURIs, "mobile-redirect-uris", cfg.MobileRedirectURIs, "JSON array of erun-mobile redirect URIs; empty creates no mobile app (env MOBILE_REDIRECT_URIS)")
	flags.DurationVar(&cfg.Interval, "interval", cfg.Interval, "Time between reconcile ticks")
	flags.BoolVar(&cfg.Once, "once", os.Getenv("OIDC_BOOTSTRAP_RUN_ONCE") == "1", "Reconcile a single tick and exit, non-zero when it was incomplete (env OIDC_BOOTSTRAP_RUN_ONCE=1)")
	flags.BoolVar(&cfg.RestorePATs, "restore-pats", false, "Seed the bootstrap directory from the PATs Secret and exit")
	if err := flags.Parse(args); err != nil {
		return oidcBootstrapConfig{}, err
	}
	if flags.NArg() > 0 {
		return oidcBootstrapConfig{}, fmt.Errorf("eapi %s takes no arguments, got %q", oidcBootstrapCommand, flags.Args())
	}
	return cfg, cfg.validate()
}

func (cfg oidcBootstrapConfig) validate() error {
	required := map[string]string{
		"--pats-secret-name (PATS_SECRET_NAME)": cfg.PATsSecretName,
		"--namespace (POD_NAMESPACE)":           cfg.Namespace,
		"--bootstrap-dir (BOOTSTRAP_DIR)":       cfg.BootstrapDir,
	}
	if !cfg.RestorePATs {
		required["--external-domain (EXTERNAL_DOMAIN)"] = cfg.ExternalDomain
		required["--project-name (PROJECT_NAME)"] = cfg.ProjectName
		required["--configmap-name (CONFIGMAP_NAME)"] = cfg.ConfigMapName
	}
	var missing []string
	for name, value := range required {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("eapi %s: missing required %s", oidcBootstrapCommand, strings.Join(missing, ", "))
	}
	if !cfg.RestorePATs && cfg.Interval <= 0 {
		return errors.New("eapi " + oidcBootstrapCommand + ": --interval must be positive")
	}
	return nil
}

// reconcilerConfig decodes the redirect lists into the reconciler's input.
func (cfg oidcBootstrapConfig) reconcilerConfig() (oidcbootstrap.Config, error) {
	console, err := parseURIList("console redirect URIs", cfg.ConsoleRedirectURIs)
	if err != nil {
		return oidcbootstrap.Config{}, err
	}
	mobile, err := parseURIList("mobile redirect URIs", cfg.MobileRedirectURIs)
	if err != nil {
		return oidcbootstrap.Config{}, err
	}
	return oidcbootstrap.Config{
		ProjectName:         cfg.ProjectName,
		ConfigMapName:       cfg.ConfigMapName,
		PATsSecretName:      cfg.PATsSecretName,
		Namespace:           cfg.Namespace,
		BootstrapDir:        cfg.BootstrapDir,
		ConsoleRedirectURIs: console,
		MobileRedirectURIs:  mobile,
	}, nil
}

func parseURIList(what string, value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return []string{}, nil
	}
	var uris []string
	if err := json.Unmarshal([]byte(value), &uris); err != nil {
		return nil, fmt.Errorf("%s must be a JSON array of strings: %w", what, err)
	}
	if uris == nil {
		uris = []string{}
	}
	return uris, nil
}

// runOIDCBootstrap is `eapi oidc-bootstrap`.
func runOIDCBootstrap(ctx context.Context, args []string) error {
	cfg, err := resolveOIDCBootstrapConfig(args)
	if err != nil {
		return err
	}
	kube, err := inClusterKubeClient()
	if err != nil {
		return err
	}
	return runOIDCBootstrapWith(ctx, cfg, kube, log.New(os.Stderr, "oidc-bootstrap: ", log.LstdFlags|log.Lmsgprefix))
}

// runOIDCBootstrapWith is runOIDCBootstrap past configuration and cluster
// wiring, so the whole flow runs in a test against a fake API server.
func runOIDCBootstrapWith(ctx context.Context, cfg oidcBootstrapConfig, kube kubernetes.Interface, logger *log.Logger) error {
	if cfg.RestorePATs {
		return oidcbootstrap.RestorePATs(ctx, kube, cfg.Namespace, cfg.PATsSecretName, cfg.BootstrapDir, logger)
	}
	reconcilerCfg, err := cfg.reconcilerConfig()
	if err != nil {
		return err
	}
	patPath := filepath.Join(cfg.BootstrapDir, oidcbootstrap.AdminPATFile)
	logger.Print("waiting for the org-owner PAT")
	if err := oidcbootstrap.WaitForFile(ctx, patPath, patPollInterval); err != nil {
		return err
	}
	idp, err := zitadel.NewClientFromFile(zitadel.Config{
		BaseURL:        fmt.Sprintf("http://localhost:%d", cfg.CorePort),
		ExternalDomain: cfg.ExternalDomain,
		PATPath:        patPath,
	})
	if err != nil {
		return err
	}
	reconciler := oidcbootstrap.NewReconciler(reconcilerCfg, idp, kube, logger)
	if cfg.Once {
		return reconciler.Reconcile(ctx)
	}
	return reconciler.Run(ctx, cfg.Interval)
}

func inClusterKubeClient() (kubernetes.Interface, error) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config for %s: %w", oidcBootstrapCommand, err)
	}
	return kubernetes.NewForConfig(restConfig)
}
