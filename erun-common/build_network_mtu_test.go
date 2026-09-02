package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stubNetRoots(t *testing.T, iface string, mtu string) (procRoot, sysRoot string) {
	t.Helper()
	root := t.TempDir()
	procRoot = filepath.Join(root, "proc")
	sysRoot = filepath.Join(root, "sys")
	if err := os.MkdirAll(filepath.Join(procRoot, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sysRoot, "class", "net", iface), 0o755); err != nil {
		t.Fatal(err)
	}
	// A decoy non-default route first, so a reader that took the first row
	// rather than the default one would be caught.
	table := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"decoy0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n" +
		iface + "\t00000000\t0128000A\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"
	if err := os.WriteFile(filepath.Join(procRoot, "net", "route"), []byte(table), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysRoot, "class", "net", iface, "mtu"), []byte(mtu+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return procRoot, sysRoot
}

func TestEgressMTUReadsTheDefaultRoutesInterface(t *testing.T) {
	procRoot, sysRoot := stubNetRoots(t, "eth0", "1450")

	mtu, iface, ok := egressMTUUnder(procRoot, sysRoot)
	if !ok {
		t.Fatal("expected the default route's MTU to resolve")
	}
	if mtu != 1450 || iface != "eth0" {
		t.Fatalf("expected eth0/1450, got %s/%d", iface, mtu)
	}
}

func TestEgressMTUFailsWithNoDefaultRoute(t *testing.T) {
	root := t.TempDir()
	procRoot := filepath.Join(root, "proc")
	if err := os.MkdirAll(filepath.Join(procRoot, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procRoot, "net", "route"), []byte("Iface\tDestination\tGateway\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := egressMTUUnder(procRoot, filepath.Join(root, "sys")); ok {
		t.Fatal("with no default route nothing should be claimed")
	}
}

func TestParsePositiveMTURejectsImplausibleValues(t *testing.T) {
	for _, raw := range []string{"", "nope", "0", "68", "1279", "-1500"} {
		if _, ok := parsePositiveMTU(raw); ok {
			t.Fatalf("%q should not parse as a usable MTU", raw)
		}
	}
	if mtu, ok := parsePositiveMTU("1280"); !ok || mtu != 1280 {
		t.Fatal("the IPv6 minimum is a real link MTU and should parse")
	}
}

// The mismatch is only claimed when both numbers were genuinely read and the
// bridge is the larger. Every other combination has to stay silent, because a
// wrong confident diagnosis is worse than none (root AGENTS.md).
func TestDockerBridgeMTUMismatchOnlyFiresOnAMeasuredMismatch(t *testing.T) {
	inPod := func(string) string { return "10.43.0.1" }
	offPod := func(string) string { return "" }
	bridge := func(v int, ok bool) func() (int, bool) {
		return func() (int, bool) { return v, ok }
	}
	egress := func(v int, ok bool) func() (int, string, bool) {
		return func() (int, string, bool) { return v, "eth0", ok }
	}

	tests := []struct {
		name   string
		getenv func(string) string
		bridge func() (int, bool)
		egress func() (int, string, bool)
		want   bool
	}{
		{"bridge wider than the pod network", inPod, bridge(1500, true), egress(1450, true), true},
		{"bridge already fits", inPod, bridge(1450, true), egress(1450, true), false},
		{"bridge narrower than the pod network", inPod, bridge(1400, true), egress(1450, true), false},
		{"off-pod, where the two are unrelated", offPod, bridge(1500, true), egress(1450, true), false},
		{"bridge MTU unreadable", inPod, bridge(0, false), egress(1450, true), false},
		{"pod MTU unreadable", inPod, bridge(1500, true), egress(0, false), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mismatch, ok := dockerBridgeMTUMismatchWith(test.getenv, test.bridge, test.egress)
			if ok != test.want {
				t.Fatalf("expected mismatch=%v, got %v", test.want, ok)
			}
			if ok && (mismatch.BridgeMTU != 1500 || mismatch.PodMTU != 1450) {
				t.Fatalf("the reported pair should be the measured one, got %+v", mismatch)
			}
		})
	}
}

func TestBridgeMTUDiagnosisNamesTheCauseAndACommandThatResolvesIt(t *testing.T) {
	t.Setenv("ERUN_TENANT", "erun")
	t.Setenv("ERUN_ENVIRONMENT", "code3")

	diagnosis := bridgeMTUMismatch{BridgeMTU: 1500, PodMTU: 1450, Interface: "eth0"}.diagnosis()
	for _, want := range []string{"1500", "1450", "eth0", "erun deploy erun code3"} {
		if !strings.Contains(diagnosis, want) {
			t.Fatalf("the diagnosis should name %q, got: %q", want, diagnosis)
		}
	}
	if !strings.Contains(diagnosis, "not at fault") {
		t.Fatalf("the diagnosis should say the code under build is not the cause, got: %q", diagnosis)
	}
}

func TestBridgeMTUDiagnosisFallsBackWhenTheEnvironmentIsUnknown(t *testing.T) {
	t.Setenv("ERUN_TENANT", "")
	t.Setenv("ERUN_ENVIRONMENT", "")

	diagnosis := bridgeMTUMismatch{BridgeMTU: 1500, PodMTU: 1450, Interface: "eth0"}.diagnosis()
	if !strings.Contains(diagnosis, "<tenant> <environment>") {
		t.Fatalf("with no environment the remedy should stay a template, got: %q", diagnosis)
	}
}

// A stall is necessary but not sufficient, and so is the mismatch. Only their
// coincidence is evidence, so the marker set must not drag in a server's own
// answer -- a 404 on a pinned URL is a real defect in the build.
func TestDockerBuildOutputShowsNetworkStall(t *testing.T) {
	stalls := []string{
		"curl: (35) Recv failure: Connection reset by peer",
		"curl: (28) Connection timed out after 30002 milliseconds",
		"net/http: TLS handshake timeout",
		"E: Failed to fetch http://deb.debian.org/... Connection failed",
	}
	for _, output := range stalls {
		if !dockerBuildOutputShowsNetworkStall(output) {
			t.Fatalf("expected a network stall to be recognised: %q", output)
		}
	}

	notStalls := []string{
		"curl: (22) The requested URL returned error: 404",
		"undefined: someSymbol",
		"FAIL\tgithub.com/sophium/erun/erun-common\t0.4s",
		"npm ERR! code E403",
	}
	for _, output := range notStalls {
		if dockerBuildOutputShowsNetworkStall(output) {
			t.Fatalf("a real build failure must not be attributed to the network: %q", output)
		}
	}
}
