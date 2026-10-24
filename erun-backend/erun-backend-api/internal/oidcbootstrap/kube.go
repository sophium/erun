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

// applySecret creates the Opaque Secret, or replaces its data when it differs.
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

// RestorePATs seeds dir from the PATs Secret, since core writes them only at
// first init. A missing Secret is the first init, not an error.
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
		// Core, the login container and the sidecar run as different users.
		if err := os.WriteFile(filepath.Join(dir, file), content, 0o644); err != nil {
			return fmt.Errorf("restore %s: %w", file, err)
		}
	}
	return nil
}

// WaitForFile blocks until path has non-empty content or ctx is done.
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

// Run reconciles now and every interval; failed ticks are retried, so state
// deleted out of band is restored.
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
