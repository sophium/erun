package eruncommon

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ObservedCertificate is a cert-manager Certificate's readiness. Orders is
// populated only when Ready is false: that is the failure-chain walk this
// command exists for, so a caller never issues three more calls to reach the
// reason a stuck issuance is stuck.
type ObservedCertificate struct {
	Name       string                     `json:"name"`
	Ready      bool                       `json:"ready"`
	Reason     string                     `json:"reason,omitempty"`
	Message    string                     `json:"message,omitempty"`
	SecretName string                     `json:"secretName,omitempty"`
	DNSNames   []string                   `json:"dnsNames,omitempty"`
	Orders     []ObservedCertificateOrder `json:"orders,omitempty"`
}

type ObservedCertificateOrder struct {
	Name       string                         `json:"name"`
	State      string                         `json:"state,omitempty"`
	Reason     string                         `json:"reason,omitempty"`
	Challenges []ObservedCertificateChallenge `json:"challenges,omitempty"`
}

type ObservedCertificateChallenge struct {
	Name    string `json:"name"`
	Type    string `json:"type,omitempty"`
	DNSName string `json:"dnsName,omitempty"`
	State   string `json:"state,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// certificateList/certificateItem is a deliberately partial parse of
// `kubectl get certificates.cert-manager.io -o json`.
type certificateList struct {
	Items []certificateItem `json:"items"`
}

type certificateItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		SecretName string   `json:"secretName"`
		DNSNames   []string `json:"dnsNames"`
	} `json:"spec"`
	Status struct {
		Conditions []certManagerCondition `json:"conditions"`
	} `json:"status"`
}

type certManagerCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// ownedResourceItem is shared across CertificateRequest, Order, and
// Challenge: each has metadata/ownerReferences, and Order/Challenge share the
// same state/reason status shape and a spec.type/dnsName (only Challenge sets
// them). Fields the current kind does not have simply stay zero-valued.
type ownedResourceList struct {
	Items []ownedResourceItem `json:"items"`
}

type ownedResourceItem struct {
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

func certificateReady(item certificateItem) (ready bool, reason, message string) {
	for _, cond := range item.Status.Conditions {
		if cond.Type == "Ready" {
			return cond.Status == "True", cond.Reason, cond.Message
		}
	}
	return false, "", ""
}

func anyCertificateNotReady(certs []certificateItem) bool {
	for _, cert := range certs {
		if ready, _, _ := certificateReady(cert); !ready {
			return true
		}
	}
	return false
}

func ownerReferenceName(item ownedResourceItem, kind string) (string, bool) {
	for _, ref := range item.Metadata.OwnerReferences {
		if ref.Kind == kind {
			return ref.Name, true
		}
	}
	return "", false
}

// latestCertificateRequestName returns the most recently created
// CertificateRequest cert-manager made for the named Certificate — the label
// cert-manager.io/certificate-name is how cert-manager itself links them,
// since a Certificate's status carries no direct pointer to it. RFC3339
// creation timestamps sort correctly as plain strings.
func latestCertificateRequestName(requests []ownedResourceItem, certificateName string) (string, bool) {
	var latest ownedResourceItem
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

func resourcesOwnedBy(resources []ownedResourceItem, ownerKind, ownerName string) []ownedResourceItem {
	var owned []ownedResourceItem
	for _, resource := range resources {
		if name, ok := ownerReferenceName(resource, ownerKind); ok && name == ownerName {
			owned = append(owned, resource)
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].Metadata.Name < owned[j].Metadata.Name })
	return owned
}

func buildObservedCertificates(certs []certificateItem, requests, orders, challenges []ownedResourceItem) []ObservedCertificate {
	result := make([]ObservedCertificate, 0, len(certs))
	for _, cert := range certs {
		ready, reason, message := certificateReady(cert)
		observed := ObservedCertificate{
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

func observedOrdersForCertificate(certificateName string, requests, orders, challenges []ownedResourceItem) []ObservedCertificateOrder {
	crName, ok := latestCertificateRequestName(requests, certificateName)
	if !ok {
		return nil
	}
	var result []ObservedCertificateOrder
	for _, order := range resourcesOwnedBy(orders, "CertificateRequest", crName) {
		oo := ObservedCertificateOrder{Name: order.Metadata.Name, State: order.Status.State, Reason: order.Status.Reason}
		for _, challenge := range resourcesOwnedBy(challenges, "Order", order.Metadata.Name) {
			oo.Challenges = append(oo.Challenges, ObservedCertificateChallenge{
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

func certificateChainArgs(req ShellLaunchParams) (certificateRequests, orders, challenges []string) {
	return observeGetArgs(req, "certificaterequests.cert-manager.io"),
		observeGetArgs(req, "orders.acme.cert-manager.io"),
		observeGetArgs(req, "challenges.acme.cert-manager.io")
}

func parseOwnedResourceList(raw []byte) ([]ownedResourceItem, error) {
	var list ownedResourceList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func fetchOwnedResourceList(args []string, kind string) ([]ownedResourceItem, error) {
	raw, stderr, err := runObserveKubectl(args)
	if err != nil {
		if isKubectlUnknownResource(stderr) {
			return nil, nil
		}
		return nil, fmt.Errorf("observe: get %s: %w", kind, kubectlErrorMessage(err, stderr))
	}
	return parseOwnedResourceList(raw)
}

// fetchObservedCertificates reads Certificates and, only for ones that are not
// Ready, walks CertificateRequest -> Order -> Challenge for the failure
// reason. ctx/req are needed here (unlike the other observe fetchers) because
// that walk is itself conditional on what the first read finds, so its
// kubectl calls are traced only when they actually run.
func fetchObservedCertificates(ctx Context, req ShellLaunchParams, certArgs []string) ([]ObservedCertificate, error) {
	raw, stderr, err := runObserveKubectl(certArgs)
	if err != nil {
		if isKubectlUnknownResource(stderr) {
			return nil, nil
		}
		return nil, fmt.Errorf("observe: get certificates: %w", kubectlErrorMessage(err, stderr))
	}
	var list certificateList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("observe: parse certificates: %w", err)
	}
	if !anyCertificateNotReady(list.Items) {
		return buildObservedCertificates(list.Items, nil, nil, nil), nil
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
	return buildObservedCertificates(list.Items, requests, orders, challenges), nil
}
