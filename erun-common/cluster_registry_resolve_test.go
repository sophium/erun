package eruncommon

import "testing"

func TestClusterRegistryResolverInCluster(t *testing.T) {
	resolve := NewClusterRegistryResolver("erun-k3s", ClusterRegistryDeps{
		InCluster:       true,
		LookupClusterIP: func(_, _, _ string) (string, error) { return "10.43.0.50", nil },
		StartPortForward: func(_, _, _ string, _ int) (int, error) {
			t.Fatal("in-cluster build must not start a port-forward")
			return 0, nil
		},
	})
	addrs, err := resolve(ClusterRegistry{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if addrs.Push != "10.43.0.50:5000" || addrs.Pull != "10.43.0.50:5000" {
		t.Errorf("in-cluster addrs = %+v, want push=pull=10.43.0.50:5000", addrs)
	}
}

func TestClusterRegistryResolverHostPortForward(t *testing.T) {
	resolve := NewClusterRegistryResolver("erun-k3s", ClusterRegistryDeps{
		InCluster:        false,
		LookupClusterIP:  func(_, _, _ string) (string, error) { return "10.43.0.50", nil },
		StartPortForward: func(_, _, _ string, _ int) (int, error) { return 41000, nil },
	})
	addrs, err := resolve(ClusterRegistry{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if addrs.Push != "localhost:41000" {
		t.Errorf("host push = %q, want localhost:41000", addrs.Push)
	}
	if addrs.Pull != "10.43.0.50:5000" {
		t.Errorf("host pull = %q, want the in-cluster ClusterIP", addrs.Pull)
	}
}

func TestClusterRegistryResolverEmptyClusterIP(t *testing.T) {
	resolve := NewClusterRegistryResolver("erun-k3s", ClusterRegistryDeps{
		InCluster:       true,
		LookupClusterIP: func(_, _, _ string) (string, error) { return "", nil },
	})
	if _, err := resolve(ClusterRegistry{}); err == nil {
		t.Fatal("expected error when the registry Service has no ClusterIP")
	}
}
