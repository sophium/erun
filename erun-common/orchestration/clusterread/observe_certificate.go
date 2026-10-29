package clusterread

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy/observe"
)

type certificateList struct {
	Items []CertificateItem `json:"items"`
}

type CertificateItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		SecretName string   `json:"secretName"`
		DNSNames   []string `json:"dnsNames"`
	} `json:"spec"`
	Status struct {
		Conditions []CertManagerCondition `json:"conditions"`
	} `json:"status"`
}

type CertManagerCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// ownedResourceItem is shared across CertificateRequest, Order and Challenge; absent fields stay zero.
type ownedResourceList struct {
	Items []OwnedResourceItem `json:"items"`
}

type OwnedResourceItem struct {
	Metadata struct {
		Name              string            `json:"name"`
		CreationTimestamp string            `json:"creationTimestamp"`
		Labels            map[string]string `json:"labels"`
		OwnerReferences   []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"ownerReferences"`
	} `json:"metadata"`
	Spec struct {
		Type    string `json:"type"`
		DNSName string `json:"dnsName"`
	} `json:"spec"`
	Status struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	} `json:"status"`
}

func certificateReady(item CertificateItem) (ready bool, reason, message string) {
	for _, cond := range item.Status.Conditions {
		if cond.Type == "Ready" {
			return cond.Status == "True", cond.Reason, cond.Message
		}
	}
	return false, "", ""
}

func anyCertificateNotReady(certs []CertificateItem) bool {
	for _, cert := range certs {
		if ready, _, _ := certificateReady(cert); !ready {
			return true
		}
	}
	return false
}

func ownerReferenceName(item OwnedResourceItem, kind string) (string, bool) {
	for _, ref := range item.Metadata.OwnerReferences {
		if ref.Kind == kind {
			return ref.Name, true
		}
	}
	return "", false
}

// LatestCertificateRequestName returns the newest CertificateRequest labeled with the
// Certificate's name; RFC3339 timestamps sort as strings.
func LatestCertificateRequestName(requests []OwnedResourceItem, certificateName string) (string, bool) {
	var latest OwnedResourceItem
	found := false
	for _, cr := range requests {
		if cr.Metadata.Labels["cert-manager.io/certificate-name"] != certificateName {
			continue
		}
		if !found || cr.Metadata.CreationTimestamp > latest.Metadata.CreationTimestamp {
			latest = cr
			found = true
		}
	}
	return latest.Metadata.Name, found
}

func resourcesOwnedBy(resources []OwnedResourceItem, ownerKind, ownerName string) []OwnedResourceItem {
	var owned []OwnedResourceItem
	for _, resource := range resources {
		if name, ok := ownerReferenceName(resource, ownerKind); ok && name == ownerName {
			owned = append(owned, resource)
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].Metadata.Name < owned[j].Metadata.Name })
	return owned
}

func BuildObservedCertificates(certs []CertificateItem, requests, orders, challenges []OwnedResourceItem) []model.ObservedCertificate {
	result := make([]model.ObservedCertificate, 0, len(certs))
	for _, cert := range certs {
		ready, reason, message := certificateReady(cert)
		observed := model.ObservedCertificate{
			Name:       cert.Metadata.Name,
			Ready:      ready,
			Reason:     reason,
			Message:    message,
			SecretName: cert.Spec.SecretName,
			DNSNames:   cert.Spec.DNSNames,
		}
		if !ready {
			observed.Orders = observedOrdersForCertificate(cert.Metadata.Name, requests, orders, challenges)
		}
		result = append(result, observed)
	}
	return result
}

func observedOrdersForCertificate(certificateName string, requests, orders, challenges []OwnedResourceItem) []model.ObservedCertificateOrder {
	crName, ok := LatestCertificateRequestName(requests, certificateName)
	if !ok {
		return nil
	}
	var result []model.ObservedCertificateOrder
	for _, order := range resourcesOwnedBy(orders, "CertificateRequest", crName) {
		oo := model.ObservedCertificateOrder{Name: order.Metadata.Name, State: order.Status.State, Reason: order.Status.Reason}
		for _, challenge := range resourcesOwnedBy(challenges, "Order", order.Metadata.Name) {
			oo.Challenges = append(oo.Challenges, model.ObservedCertificateChallenge{
				Name:    challenge.Metadata.Name,
				Type:    challenge.Spec.Type,
				DNSName: challenge.Spec.DNSName,
				State:   challenge.Status.State,
				Reason:  challenge.Status.Reason,
			})
		}
		result = append(result, oo)
	}
	return result
}

func certificateChainArgs(req config.ShellLaunchParams) (certificateRequests, orders, challenges []string) {
	return ObserveGetArgs(req, "certificaterequests.cert-manager.io"),
		ObserveGetArgs(req, "orders.acme.cert-manager.io"),
		ObserveGetArgs(req, "challenges.acme.cert-manager.io")
}

func parseOwnedResourceList(raw []byte) ([]OwnedResourceItem, error) {
	var list ownedResourceList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func fetchOwnedResourceList(args []string, kind string) ([]OwnedResourceItem, error) {
	raw, stderr, err := observe.RunObserveKubectl(args)
	if err != nil {
		if isKubectlUnknownResource(stderr) {
			return nil, nil
		}
		return nil, fmt.Errorf("observe: get %s: %w", kind, observe.KubectlErrorMessage(err, stderr))
	}
	return parseOwnedResourceList(raw)
}

// FetchObservedCertificates reads Certificates and walks the failure chain only for unready ones.
func FetchObservedCertificates(ctx core.Context, req config.ShellLaunchParams, certArgs []string) ([]model.ObservedCertificate, error) {
	raw, stderr, err := observe.RunObserveKubectl(certArgs)
	if err != nil {
		if isKubectlUnknownResource(stderr) {
			return nil, nil
		}
		return nil, fmt.Errorf("observe: get certificates: %w", observe.KubectlErrorMessage(err, stderr))
	}
	var list certificateList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("observe: parse certificates: %w", err)
	}
	if !anyCertificateNotReady(list.Items) {
		return BuildObservedCertificates(list.Items, nil, nil, nil), nil
	}

	crArgs, orderArgs, challengeArgs := certificateChainArgs(req)
	ctx.TraceCommand("", "kubectl", crArgs...)
	ctx.TraceCommand("", "kubectl", orderArgs...)
	ctx.TraceCommand("", "kubectl", challengeArgs...)

	requests, err := fetchOwnedResourceList(crArgs, "certificaterequests")
	if err != nil {
		return nil, err
	}
	orders, err := fetchOwnedResourceList(orderArgs, "orders")
	if err != nil {
		return nil, err
	}
	challenges, err := fetchOwnedResourceList(challengeArgs, "challenges")
	if err != nil {
		return nil, err
	}
	return BuildObservedCertificates(list.Items, requests, orders, challenges), nil
}
