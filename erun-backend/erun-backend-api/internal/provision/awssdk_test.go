package provision

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// fakeAWS is an HTTP endpoint that speaks the real EC2/IAM query and SSM JSON
// wire protocols. Pointing the runner's BaseEndpoint at it exercises the whole
// SDK path — request signing, input serialization, response parsing — without
// reaching AWS. It models a fresh account: no role, no instance profile, and no
// instance yet tagged for the context.
type fakeAWS struct {
	mu       sync.Mutex
	requests []awsRequest
}

type awsRequest struct {
	action string
	form   url.Values
	body   string
}

func (f *fakeAWS) record(req awsRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
}

func (f *fakeAWS) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.requests))
	for _, req := range f.requests {
		names = append(names, req.action)
	}
	return names
}

// find returns the first recorded request for an action.
func (f *fakeAWS) find(t *testing.T, action string) awsRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, req := range f.requests {
		if req.action == action {
			return req
		}
	}
	t.Fatalf("no %s request was made; got %v", action, f.actions())
	return awsRequest{}
}

func (f *fakeAWS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	body := string(raw)
	if target := r.Header.Get("X-Amz-Target"); target != "" {
		f.record(awsRequest{action: target, body: body})
		f.serveSSM(w, target)
		return
	}
	form, _ := url.ParseQuery(body)
	action := form.Get("Action")
	f.record(awsRequest{action: action, form: form})
	f.serveQuery(w, action, form)
}

func (f *fakeAWS) serveSSM(w http.ResponseWriter, target string) {
	if !strings.HasSuffix(target, "GetParameter") {
		writeJSONError(w, http.StatusBadRequest, "ValidationException", target)
		return
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"Parameter": map[string]any{"Name": "ubuntu-ami", "Value": fakeAMI},
	})
}

const (
	fakeAMI        = "ami-0fakeubuntuarm64"
	fakeGroupID    = "sg-0fake"
	fakeInstanceID = "i-0fakeinstance"
	fakePublicIP   = "203.0.113.7"
	fakeProfileARN = "arn:aws:iam::123456789012:instance-profile/erun-test-host-stop"
)

func (f *fakeAWS) serveQuery(w http.ResponseWriter, action string, form url.Values) {
	w.Header().Set("Content-Type", "text/xml")
	switch action {
	case "CreateSecurityGroup":
		writeXML(w, `<CreateSecurityGroupResponse><groupId>`+fakeGroupID+`</groupId></CreateSecurityGroupResponse>`)
	case "AuthorizeSecurityGroupIngress":
		writeXML(w, `<AuthorizeSecurityGroupIngressResponse><return>true</return></AuthorizeSecurityGroupIngressResponse>`)
	case "GetRole", "GetInstanceProfile":
		// A fresh account: neither exists yet, so the bootstrap takes its create
		// path — the same branch a first provision for a tenant takes.
		writeQueryError(w, http.StatusNotFound, "NoSuchEntity", action+" not found")
	case "CreateRole":
		writeXML(w, `<CreateRoleResponse><CreateRoleResult><Role><RoleName>`+form.Get("RoleName")+`</RoleName></Role></CreateRoleResult></CreateRoleResponse>`)
	case "PutRolePolicy":
		writeXML(w, `<PutRolePolicyResponse/>`)
	case "CreateInstanceProfile":
		writeXML(w, `<CreateInstanceProfileResponse><CreateInstanceProfileResult><InstanceProfile><Arn>`+fakeProfileARN+`</Arn><InstanceProfileName>`+form.Get("InstanceProfileName")+`</InstanceProfileName></InstanceProfile></CreateInstanceProfileResult></CreateInstanceProfileResponse>`)
	case "AddRoleToInstanceProfile":
		writeXML(w, `<AddRoleToInstanceProfileResponse/>`)
	case "RunInstances":
		writeXML(w, `<RunInstancesResponse><instancesSet><item><instanceId>`+fakeInstanceID+`</instanceId></item></instancesSet></RunInstancesResponse>`)
	case "DescribeInstances":
		f.serveDescribeInstances(w, form)
	default:
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", "unexpected action "+action)
	}
}

// serveDescribeInstances distinguishes the pre-launch idempotency lookup (by
// tag filter, and empty here) from the post-launch waiter and public-IP reads
// (by instance id).
func (f *fakeAWS) serveDescribeInstances(w http.ResponseWriter, form url.Values) {
	if form.Get("InstanceId.1") == "" {
		writeXML(w, `<DescribeInstancesResponse><reservationSet/></DescribeInstancesResponse>`)
		return
	}
	writeXML(w, `<DescribeInstancesResponse><reservationSet><item><instancesSet><item>`+
		`<instanceId>`+fakeInstanceID+`</instanceId>`+
		`<instanceState><code>16</code><name>running</name></instanceState>`+
		`<ipAddress>`+fakePublicIP+`</ipAddress>`+
		`</item></instancesSet></item></reservationSet></DescribeInstancesResponse>`)
}

func writeXML(w http.ResponseWriter, body string) {
	_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>`+body)
}

func writeQueryError(w http.ResponseWriter, status int, code, message string) {
	w.WriteHeader(status)
	writeXML(w, `<ErrorResponse><Error><Type>Sender</Type><Code>`+code+`</Code><Message>`+message+`</Message></Error></ErrorResponse>`)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"__type": code, "message": message})
}

// isolateAWSEnvironment keeps the host's own AWS configuration out of the
// resolved config, so the test proves what the alias supplies rather than what
// the developer's machine happens to hold.
func isolateAWSEnvironment(t *testing.T) {
	t.Helper()
	missing := t.TempDir() + "/absent"
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", missing)
	t.Setenv("AWS_CONFIG_FILE", missing)
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

// TestInitCloudContextThroughSDKRunner drives the shared bootstrap sequence
// end-to-end over the SDK runner and asserts the resolved identity it hands
// back, so the argv-to-SDK translation and the rendering the sequence re-parses
// are both pinned.
func TestInitCloudContextThroughSDKRunner(t *testing.T) {
	isolateAWSEnvironment(t)
	fake := &fakeAWS{}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	runner, err := newAWSSDKRunner(`{"accessKeyId":"test","secretAccessKey":"test"}`, server.URL)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	status, err := eruncommon.InitCloudContext(
		eruncommon.Context{
			Logger: eruncommon.NewLoggerWithWriters(eruncommon.VerbosityInfo, io.Discard, io.Discard),
			Stdout: io.Discard,
			Stderr: io.Discard,
		},
		aliasStore{alias: "acme-aws", provider: eruncommon.CloudProviderAWS},
		eruncommon.InitCloudContextParams{
			Name:               "erun-test",
			CloudProviderAlias: "acme-aws",
			Region:             eruncommon.DefaultCloudContextRegion,
			InstanceType:       eruncommon.DefaultCloudContextInstanceType,
			DiskType:           eruncommon.DefaultCloudContextDiskType,
			DiskSizeGB:         eruncommon.DefaultCloudContextDiskSizeGB,
		},
		eruncommon.CloudContextDependencies{
			RunAWS:     runner.runAWS(t.Context()),
			RunKubectl: func(eruncommon.Context, []string) error { return nil },
			NewToken:   func() string { return "test-admin-token" },
		},
	)
	if err != nil {
		t.Fatalf("InitCloudContext: %v (calls: %v)", err, fake.actions())
	}

	if status.InstanceID != fakeInstanceID {
		t.Errorf("instance id = %q, want %q", status.InstanceID, fakeInstanceID)
	}
	if status.PublicIP != fakePublicIP {
		t.Errorf("public ip = %q, want %q", status.PublicIP, fakePublicIP)
	}
	if status.SecurityGroupID != fakeGroupID {
		t.Errorf("security group = %q, want %q", status.SecurityGroupID, fakeGroupID)
	}
	if status.InstanceProfileARN != fakeProfileARN {
		t.Errorf("instance profile arn = %q, want %q", status.InstanceProfileARN, fakeProfileARN)
	}

	// Every call the bootstrap makes must have reached AWS as a real API call.
	for _, want := range []string{
		"CreateSecurityGroup", "AuthorizeSecurityGroupIngress",
		"GetRole", "CreateRole", "PutRolePolicy",
		"GetInstanceProfile", "CreateInstanceProfile", "AddRoleToInstanceProfile",
		"DescribeInstances", "RunInstances", "AmazonSSM.GetParameter",
	} {
		if !containsString(fake.actions(), want) {
			t.Errorf("no %s call was made; got %v", want, fake.actions())
		}
	}
}

// TestRunInstancesTranslatesShorthandArgs pins the translation of the aws-CLI
// shorthand the shared sequence emits into typed RunInstances input: getting
// any of these wrong launches a usable-looking instance with the wrong disk,
// tags, or IMDS policy.
func TestRunInstancesTranslatesShorthandArgs(t *testing.T) {
	isolateAWSEnvironment(t)
	fake := &fakeAWS{}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	userData := t.TempDir() + "/user-data.sh"
	writeFile(t, userData, "#!/bin/sh\necho k3s\n")

	runner, err := newAWSSDKRunner(`{"accessKeyId":"test","secretAccessKey":"test","sessionToken":"tok"}`, server.URL)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	out, err := runner.run(t.Context(), "eu-west-2", []string{
		"ec2", "run-instances",
		"--image-id", fakeAMI,
		"--instance-type", "c8gd.2xlarge",
		"--count", "1",
		"--block-device-mappings", "DeviceName=/dev/sda1,Ebs={VolumeSize=100,VolumeType=gp3,DeleteOnTermination=true}",
		"--user-data", "file://" + userData,
		"--metadata-options", "HttpEndpoint=enabled,HttpTokens=required,HttpPutResponseHopLimit=2",
		"--tag-specifications", "ResourceType=instance,Tags=[{Key=Name,Value=erun-test},{Key=erun:context,Value=erun-test}]",
		"--query", "Instances[0].InstanceId",
		"--output", "text",
		"--security-group-ids", fakeGroupID,
		"--iam-instance-profile", "Name=erun-test-host-stop",
	})
	if err != nil {
		t.Fatalf("run-instances: %v", err)
	}
	if out != fakeInstanceID {
		t.Fatalf("instance id = %q, want %q", out, fakeInstanceID)
	}

	form := fake.find(t, "RunInstances").form
	wantEncoded := base64.StdEncoding.EncodeToString([]byte("#!/bin/sh\necho k3s\n"))
	for field, want := range map[string]string{
		"ImageId":                             fakeAMI,
		"InstanceType":                        "c8gd.2xlarge",
		"MinCount":                            "1",
		"MaxCount":                            "1",
		"UserData":                            wantEncoded,
		"BlockDeviceMapping.1.DeviceName":     "/dev/sda1",
		"BlockDeviceMapping.1.Ebs.VolumeSize": "100",
		"BlockDeviceMapping.1.Ebs.VolumeType": "gp3",
		"BlockDeviceMapping.1.Ebs.DeleteOnTermination": "true",
		"MetadataOptions.HttpEndpoint":                 "enabled",
		"MetadataOptions.HttpTokens":                   "required",
		"MetadataOptions.HttpPutResponseHopLimit":      "2",
		"TagSpecification.1.ResourceType":              "instance",
		"TagSpecification.1.Tag.1.Key":                 "Name",
		"TagSpecification.1.Tag.1.Value":               "erun-test",
		"TagSpecification.1.Tag.2.Key":                 "erun:context",
		"TagSpecification.1.Tag.2.Value":               "erun-test",
		"SecurityGroupId.1":                            fakeGroupID,
		"IamInstanceProfile.Name":                      "erun-test-host-stop",
	} {
		if got := form.Get(field); got != want {
			t.Errorf("RunInstances %s = %q, want %q (form: %v)", field, got, want, form)
		}
	}
}

// TestRunnerRejectsUnusableCredentials pins the deliberate difference from the
// aws-CLI path: an alias without keys fails naming that, instead of silently
// provisioning as whatever ambient identity the control-plane pod holds.
func TestRunnerRejectsUnusableCredentials(t *testing.T) {
	for name, credentials := range map[string]string{
		"not json":        "not-json",
		"empty object":    `{}`,
		"no secret":       `{"accessKeyId":"AKIA"}`,
		"no access key":   `{"secretAccessKey":"s"}`,
		"blank access id": `{"accessKeyId":"  ","secretAccessKey":"s"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newAWSSDKRunner(credentials, ""); err == nil {
				t.Fatal("want an error for unusable credentials, got nil")
			}
		})
	}
}

// TestUnsupportedArgvIsAnError keeps the runner from silently succeeding on an
// argv it does not understand: a shared sequence that grows a new call must fail
// loudly here rather than half-provision a tenant.
func TestUnsupportedArgvIsAnError(t *testing.T) {
	isolateAWSEnvironment(t)
	runner, err := newAWSSDKRunner(`{"accessKeyId":"test","secretAccessKey":"test"}`, "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	for name, args := range map[string][]string{
		"unknown service":   {"s3", "ls"},
		"unknown ec2 verb":  {"ec2", "terminate-instances", "--instance-ids", "i-1"},
		"unknown iam verb":  {"iam", "delete-role", "--role-name", "r"},
		"unknown ssm verb":  {"ssm", "put-parameter", "--name", "n"},
		"unknown waiter":    {"ec2", "wait", "instance-terminated", "--instance-ids", "i-1"},
		"too few arguments": {"ec2"},
		"unsupported query": {"ec2", "describe-instances", "--instance-ids", "i-1", "--query", "Reservations[0].Instances[0].KeyName"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runner.run(t.Context(), "eu-west-2", args); err == nil {
				t.Fatalf("want an error for %v, got nil", args)
			}
		})
	}
}

// TestParseEC2Filters pins the shorthand the shared sequence uses to find an
// instance already tagged for a context — the check that keeps a resumed
// provision from launching a duplicate.
func TestParseEC2Filters(t *testing.T) {
	filters := parseEC2Filters([]string{
		"Name=tag:erun:context,Values=erun-test",
		"Name=instance-state-name,Values=pending,running",
	})
	if len(filters) != 2 {
		t.Fatalf("got %d filters, want 2: %+v", len(filters), filters)
	}
	if *filters[0].Name != "tag:erun:context" || len(filters[0].Values) != 1 || filters[0].Values[0] != "erun-test" {
		t.Errorf("filter 0 = %+v", filters[0])
	}
	if *filters[1].Name != "instance-state-name" || strings.Join(filters[1].Values, ",") != "pending,running" {
		t.Errorf("filter 1 = %+v", filters[1])
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
