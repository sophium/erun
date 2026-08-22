// placement.go carries a lifecycle Job's target cluster (#1112): the
// deploy/stop/delete Job runs in the platform's own cluster as always, but
// its `erun` command authenticates against whichever cluster the
// environment was placed on, via a kubeconfig this package seeds.
package deployexec

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/jobexec"
)

// PlacementCredentialResolver resolves a context's live k3s admin token
// immediately before a deploy/stop/delete Job needs it (#1112) — satisfied by
// repository.ContextCredentialRepository.Get. Callers (service.
// EnvironmentProvisioner, provision.EnvLifecycle) resolve it fresh on every
// run, including a resumed one, so it is never part of a checkpointed DBOS
// workflow input and a rotated token reaches the very next Job with no
// separate sync step.
type PlacementCredentialResolver interface {
	Get(ctx context.Context, contextID string) (string, error)
}

// ResolvePlacementToken fetches the admin token for a remote placement and
// fails clearly when one is expected but cannot be resolved — a context this
// tenant registered but whose credential this server instance cannot reach
// (resolver nil, e.g. no cipher configured) must not silently deploy without
// authenticating, which would surface only as an opaque kubectl failure deep
// inside the Job.
func ResolvePlacementToken(ctx context.Context, resolver PlacementCredentialResolver, contextID string) (string, error) {
	if contextID == "" {
		return "", nil
	}
	if resolver == nil {
		return "", fmt.Errorf("no placement credential resolver is configured for context %q", contextID)
	}
	return resolver.Get(ctx, contextID)
}

// PlacementParams names the cluster a lifecycle Job's `erun` command targets.
// The zero value keeps the Job targeting its own cluster via its
// ServiceAccount token, unchanged from before multi-cluster placement
// existed (#1112).
type PlacementParams struct {
	// ContextID is the context row's id, used only to name the Secret that
	// custodies its admin token (PlacementSecretName) — never interpolated
	// into a script.
	ContextID string
	// KubernetesContext is the kubeconfig context name and the value the
	// seeded env config's kubernetescontext: reads back. Validated as a
	// DNS-1123 label at context creation (routes.decodeCreateContextInput),
	// but still passed through shellJoin here rather than trusted blind.
	KubernetesContext string
	// ServerURL is the target cluster's API server, https://<publicIP>:6443.
	ServerURL string
	// AdminToken is the target cluster's k3s admin token, resolved fresh from
	// ContextCredentialRepository immediately before a Job runs
	// (EnvironmentProvisioner.Provision / EnvLifecycle.Stop/Delete) — never
	// part of a checkpointed workflow input, never written into the Job spec.
	// ensurePlacementSecret is the only place that reads it.
	AdminToken string
}

func (p PlacementParams) remote() bool {
	return strings.TrimSpace(p.ContextID) != ""
}

// kubernetesContextOrInCluster names the context bootstrapEnvironmentScript's
// seeded env config records erun's `kubernetescontext:` as.
func (p PlacementParams) kubernetesContextOrInCluster() string {
	if p.remote() {
		return p.KubernetesContext
	}
	return "in-cluster"
}

// placementAdminTokenKey is the Secret data key holding a remote context's
// k3s admin token.
const placementAdminTokenKey = "token"

// placementAdminTokenEnvVar is the fixed name a placed Job's generated
// kubeconfig reads the token back from at runtime — never the literal token
// value, which lives only in the Secret this env var is sourced from.
const placementAdminTokenEnvVar = "ERUN_PLACEMENT_ADMIN_TOKEN"

// PlacementSecretName is deterministic in the context id, so a repeat
// deploy/stop/delete reuses (and refreshes) the same Secret instead of
// accumulating one per attempt, and a rotated token reaches the very next Job
// with no separate sync step.
func PlacementSecretName(contextID string) string {
	return "erun-ctx-cred-" + jobexec.SanitizeName(contextID)
}

// placementEnvVars returns the container Env sourcing the admin token from
// its Secret, or nil when the Job targets its own cluster — the credential
// never appears in the Job spec itself, only a Secret reference.
func placementEnvVars(placement PlacementParams) []corev1.EnvVar {
	if !placement.remote() {
		return nil
	}
	return []corev1.EnvVar{{
		Name: placementAdminTokenEnvVar,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: PlacementSecretName(placement.ContextID)},
				Key:                  placementAdminTokenKey,
			},
		},
	}}
}

// ensurePlacementSecret upserts the Secret a placed Job's container reads its
// target cluster's admin token from. Called fresh before every deploy/stop/
// delete Job that targets a remote context (Launcher.Run/RunStop/RunDelete),
// so a rotated token reaches the very next Job. A no-op when the Job targets
// its own cluster.
func ensurePlacementSecret(ctx context.Context, kube kubernetes.Interface, namespace string, placement PlacementParams) error {
	if !placement.remote() {
		return nil
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PlacementSecretName(placement.ContextID),
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "erun-deploy-executor",
				"erun.io/context":              placement.ContextID,
			},
		},
		StringData: map[string]string{placementAdminTokenKey: placement.AdminToken},
	}
	_, err := kube.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = kube.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
	}
	return err
}
