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

// PlacementCredentialResolver resolves a context's admin token fresh before each
// Job, so it never enters a checkpointed workflow input. The tenant is explicit
// because erun_operations' RLS policy is unconditional.
type PlacementCredentialResolver interface {
	Get(ctx context.Context, tenantID, contextID string) (string, error)
}

// ResolvePlacementToken fetches a remote placement's admin token, failing rather
// than deploying unauthenticated; an empty tenantID is refused.
func ResolvePlacementToken(ctx context.Context, resolver PlacementCredentialResolver, tenantID, contextID string) (string, error) {
	if contextID == "" {
		return "", nil
	}
	if strings.TrimSpace(tenantID) == "" {
		return "", fmt.Errorf("no owning tenant named for context %q: a placement credential is fetched for the tenant that owns the context", contextID)
	}
	if resolver == nil {
		return "", fmt.Errorf("no placement credential resolver is configured for context %q", contextID)
	}
	return resolver.Get(ctx, tenantID, contextID)
}

// PlacementParams names the cluster a lifecycle Job targets; the zero value is its own cluster.
type PlacementParams struct {
	// TenantID owns ContextID, so one tenant's placement cannot release another's credential.
	TenantID string
	// ContextID only names the Secret; never interpolated into a script.
	ContextID string
	// KubernetesContext is validated upstream but still passed through shellJoin.
	KubernetesContext string
	ServerURL         string
	// AdminToken is resolved fresh per Job and never written into the Job spec or workflow input.
	AdminToken string
}

func (p PlacementParams) remote() bool {
	return strings.TrimSpace(p.ContextID) != ""
}

func (p PlacementParams) kubernetesContextOrInCluster() string {
	if p.remote() {
		return p.KubernetesContext
	}
	return "in-cluster"
}

const placementAdminTokenKey = "token"

// The kubeconfig references this env var, never the literal token.
const placementAdminTokenEnvVar = "ERUN_PLACEMENT_ADMIN_TOKEN"

// PlacementSecretName is deterministic so repeat Jobs refresh one Secret.
func PlacementSecretName(contextID string) string {
	return "erun-ctx-cred-" + jobexec.SanitizeName(contextID)
}

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

// ensurePlacementSecret upserts the admin-token Secret before every remote Job,
// so a rotated token reaches the next one.
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
