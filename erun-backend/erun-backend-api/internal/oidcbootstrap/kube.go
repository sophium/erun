package oidcbootstrap

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// applyConfigMap creates the ConfigMap, or replaces its data when it differs.
// An unchanged ConfigMap is not written.
func applyConfigMap(ctx context.Context, kube kubernetes.Interface, namespace string, name string, data map[string]string) error {
	client := kube.CoreV1().ConfigMaps(namespace)
	existing, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Data:       data,
		}, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if maps.Equal(existing.Data, data) {
		return nil
	}
	existing.Data = data
	_, err = client.Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// applySecret creates the Opaque Secret, or replaces its data when it
// differs. An unchanged Secret is not written.
func applySecret(ctx context.Context, kube kubernetes.Interface, namespace string, name string, data map[string][]byte) error {
	client := kube.CoreV1().Secrets(namespace)
	existing, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Type:       corev1.SecretTypeOpaque,
			Data:       data,
		}, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if maps.EqualFunc(existing.Data, data, bytes.Equal) {
		return nil
	}
	existing.Data = data
	_, err = client.Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// RestorePATs seeds dir from the durable PATs Secret, so a restarted pod's
// login and oidc-bootstrap containers find both PATs immediately: core writes
// them only at first-instance init and an emptyDir does not survive a pod
// restart. No Secret yet is the very first init, which mints the PATs itself,
// and is not an error. Any other failure to read the Secret is, so the init
// container retries instead of starting a pod that would wait forever on PAT
// files nothing will write.
func RestorePATs(ctx context.Context, kube kubernetes.Interface, namespace string, secretName string, dir string, logger *log.Logger) error {
	secret, err := kube.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		logger.Printf("no %s Secret yet; first-instance init will mint the PATs", secretName)
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", secretName, err)
	}
	logger.Printf("restoring bootstrap PATs from %s", secretName)
	for _, file := range []string{AdminPATFile, LoginPATFile} {
		content := secret.Data[file]
		if len(content) == 0 {
			logger.Printf("%s carries no %s; leaving it for core to write", secretName, file)
			continue
		}
		// World-readable within the pod-private emptyDir, as before: core,
		// the login container and the sidecar run as different users.
		if err := os.WriteFile(filepath.Join(dir, file), content, 0o644); err != nil {
			return fmt.Errorf("restore %s: %w", file, err)
		}
	}
	return nil
}

// WaitForFile blocks until path exists with non-empty content, polling every
// interval, or until ctx is done.
func WaitForFile(ctx context.Context, path string, interval time.Duration) error {
	for {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Run reconciles once immediately and then every interval until ctx is done.
// A failed tick is logged and retried on the next one, so a ConfigMap, an app,
// or the PATs Secret deleted out of band is restored without a redeploy.
func (r *Reconciler) Run(ctx context.Context, interval time.Duration) error {
	if err := r.Reconcile(ctx); err != nil {
		r.log.Printf("initial reconcile incomplete, retrying next tick: %v", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.Reconcile(ctx); err != nil {
				r.log.Printf("reconcile failed, retrying next tick: %v", err)
			}
		}
	}
}
