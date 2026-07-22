---
name: erun-setup-k3s-cluster
description: Stand up a durable local Kubernetes cluster on Windows that erun builds and deploys to — real k3s running inside WSL2 (with an in-cluster image registry and a WSL-hosted Docker engine, no Docker Desktop), wired to an erun local-agent environment — and maintain, repair, or tear it down afterwards. Use when the user says "set up a local erun cluster on Windows", "set up k3s for erun", "create a local k3s cluster", "install k3s on Windows for erun", "run erun locally on Windows", "wire erun to a local cluster", "give me a local cluster to deploy erun to", "repair the local k3s cluster", or "tear down the local erun cluster".
---

# Local k3s cluster for erun on Windows

Give a Windows erun user a **durable** local Kubernetes cluster to `erun build` / `erun deploy` /
`erun open` against, without any cloud. It runs **real k3s** (not k3d — k3d is only the throwaway
cluster the erun test harness uses) inside a **WSL2** Ubuntu distro, alongside an in-cluster image
registry and a Docker engine, then wires an erun `local-agent` environment at it.

This whole procedure was validated end-to-end on Windows 11 24H2: k3s v1.36, Ubuntu 26.04, no Docker
Desktop. Commands are driven from Windows (PowerShell); in-cluster steps run inside the distro via
`wsl`. Windows-side installs use **Scoop**.

## Architecture (why it's shaped this way)

Everything lives in **one WSL2 distro**: k3s (its own containerd), the registry, and a Docker engine
(`docker.io`). The Windows `kubectl`/`helm`/`docker`/`erun` all reach in over `localhost`:

- `localhost:6443` → k3s API (Windows `kubectl`/`helm`/`erun`)
- `localhost:5000` → in-cluster registry (Windows `docker push` writes; k3s pulls)
- `localhost:2375` → the WSL Docker daemon (Windows `docker` CLI via `DOCKER_HOST`)

No Docker Desktop: erun needs a Docker daemon only for `build`/`push` (it shells out to `docker`), so
we run `dockerd` inside the distro and point the Windows `docker` CLI at it. That keeps one VM, one
shared `localhost`, and is fully Scoop-friendly.

**Windows↔WSL localhost needs two `.wslconfig` settings:** `networkingMode=mirrored` **and**
`[experimental] hostAddressLoopback=true`. Mirrored alone leaves the Hyper-V firewall blocking
host→WSL; `hostAddressLoopback` is what actually opens `localhost` both directions. Both are
required (Windows 11 22H2+).

## Prerequisites

- **Windows 11 22H2+** (mirrored networking + `hostAddressLoopback`). `winver` to check.
- **`kubectl`, `helm`, and the `docker` CLI** — install with Scoop:
  ```powershell
  scoop install kubectl helm docker
  ```
  (Scoop's `dockerd` is Windows-containers only; we don't use it — only the `docker` **client**,
  pointed at the WSL daemon.)
- The erun CLI on PATH (see the `erun-windows-dev` skill if it isn't).

## Step 1 — Install WSL2 + Ubuntu

WSL2 needs the **Windows Subsystem for Linux** and **Virtual Machine Platform** features, which
require elevation and a reboot the first time. In an **elevated** PowerShell:

```powershell
dism.exe /online /enable-feature /featurename:Microsoft-Windows-Subsystem-Linux /all /norestart
dism.exe /online /enable-feature /featurename:VirtualMachinePlatform /all /norestart
# Reboot now (both return exit 3010 = success, reboot required).
```

After the reboot, install WSL and Ubuntu:

```powershell
wsl --install --no-distribution   # installs the WSL platform (elevated)
wsl --install -d Ubuntu --no-launch
wsl --update
```

> **If the inbox `wsl.exe` just reprints "…is not installed" and does nothing** (seen on some builds
> even elevated), install Microsoft's WSL MSI directly instead of relying on the stub:
> ```powershell
> $rel = Invoke-RestMethod https://api.github.com/repos/microsoft/WSL/releases/latest -Headers @{'User-Agent'='erun'}
> $msi = ($rel.assets | Where-Object { $_.name -match 'x64\.msi$' -and $_.name -notmatch 'arm64' })[0]
> $out = "$env:TEMP\$($msi.name)"; Invoke-WebRequest $msi.browser_download_url -OutFile $out -Headers @{'User-Agent'='erun'}
> msiexec /i "$out" /qn /norestart
> ```
> then `wsl --install -d Ubuntu --no-launch`.
>
> **Automation note:** `dism`/`wsl --install` need a *real* elevated token; a shell that is only in
> the Administrators group (Medium integrity) gets `Error 740`. If you can't get an interactive UAC
> prompt, run the elevated step from a **Scheduled Task as `NT AUTHORITY\SYSTEM` with
> `-RunLevel Highest`** (for the WSL MSI use `msiexec`; the inbox `wsl --install` stub needs an
> interactive session and fails as SYSTEM, so prefer the MSI there).

## Step 2 — Mirrored networking + systemd, then restart

```powershell
@"
[wsl2]
networkingMode=mirrored

[experimental]
hostAddressLoopback=true
"@ | Set-Content -Encoding ascii "$env:USERPROFILE\.wslconfig"

wsl -d Ubuntu -u root -- bash -lc 'printf "[boot]\nsystemd=true\n" > /etc/wsl.conf'
wsl --shutdown        # applies both files on next boot
```

Verify systemd is init: `wsl -d Ubuntu -u root -- bash -lc 'ps -p 1 -o comm='` prints `systemd`.

## Step 3 — Provision k3s + the in-cluster registry (inside the distro)

Save this as a script and run it with `wsl -d Ubuntu -u root -- bash /mnt/c/…/setup-k3s.sh` (write it
LF-terminated). It installs iptables (k3s needs it), works around WSL2's missing `/dev/kmsg`, tells
containerd that `localhost:5000` is plain HTTP, drops in a **hostNetwork** registry (binds the node's
`:5000` directly — no CNI hostPort/portmap, which is unreliable on WSL2), and installs k3s.

```bash
#!/usr/bin/env bash
set -euxo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -y && apt-get install -y curl ca-certificates iptables

# k3s/kubelet need /dev/kmsg, which WSL2 may not provide. Create it + make it durable.
if [ ! -e /dev/kmsg ]; then ln -s /dev/console /dev/kmsg; fi
cat > /etc/systemd/system/dev-kmsg.service <<'EOF'
[Unit]
Description=Provide /dev/kmsg for k3s on WSL2
DefaultDependencies=no
Before=k3s.service
[Service]
Type=oneshot
ExecStart=/bin/sh -c 'test -e /dev/kmsg || ln -s /dev/console /dev/kmsg'
RemainAfterExit=yes
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload && systemctl enable dev-kmsg.service

# containerd: localhost:5000 (host push) and the registry ClusterIP (in-pod
# push / cluster pull for remote-agent envs) are both plain HTTP.
mkdir -p /etc/rancher/k3s
cat > /etc/rancher/k3s/registries.yaml <<'EOF'
mirrors:
  "localhost:5000":
    endpoint:
      - "http://localhost:5000"
  "10.43.0.100:5000":
    endpoint:
      - "http://10.43.0.100:5000"
EOF

# in-cluster registry on hostNetwork (binds node :5000 directly), durable hostPath
mkdir -p /var/lib/rancher/k3s/server/manifests
cat > /var/lib/rancher/k3s/server/manifests/erun-registry.yaml <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: erun-registry
  namespace: kube-system
spec:
  replicas: 1
  selector:
    matchLabels: { app: erun-registry }
  template:
    metadata:
      labels: { app: erun-registry }
    spec:
      hostNetwork: true
      containers:
        - name: registry
          image: registry:2
          env:
            - { name: REGISTRY_HTTP_ADDR, value: "0.0.0.0:5000" }
          ports:
            - { containerPort: 5000 }
          volumeMounts:
            - { name: data, mountPath: /var/lib/registry }
      volumes:
        - name: data
          hostPath: { path: /var/lib/erun-registry, type: DirectoryOrCreate }
---
# Pinned ClusterIP Service so a remote-agent env's in-pod build can push to the
# registry (pods can't reach the node's hostNetwork :5000 as localhost) and the
# cluster pulls the same address. The ClusterIP is pinned so registries.yaml
# stays valid across recreation. erun's cluster-registry resolver looks this up
# by service name, so nothing hardcodes the IP except this manifest + the mirror.
apiVersion: v1
kind: Service
metadata:
  name: erun-registry
  namespace: kube-system
spec:
  type: ClusterIP
  clusterIP: 10.43.0.100
  selector: { app: erun-registry }
  ports:
    - { name: registry, port: 5000, targetPort: 5000 }
EOF

curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--write-kubeconfig-mode=644" sh -
k3s kubectl wait --for=condition=Ready node --all --timeout=180s
k3s kubectl -n kube-system rollout status deploy/erun-registry --timeout=180s
```

## Step 4 — Docker engine inside the distro

erun's `build`/`push` shell out to `docker`. Run `dockerd` in the distro, expose it on
`127.0.0.1:2375`, and register binfmt so erun's mandatory multi-arch (`linux/amd64` + `linux/arm64`)
builds work:

```bash
#!/usr/bin/env bash
set -euxo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get install -y docker.io
mkdir -p /etc/systemd/system/docker.service.d
cat > /etc/systemd/system/docker.service.d/override.conf <<'EOF'
[Service]
ExecStart=
ExecStart=/usr/bin/dockerd -H unix:///var/run/docker.sock -H tcp://127.0.0.1:2375 --containerd=/run/containerd/containerd.sock
EOF
systemctl daemon-reload && systemctl enable docker && systemctl restart docker
for i in $(seq 1 10); do docker version >/dev/null 2>&1 && break; sleep 2; done
docker run --privileged --rm tonistiigi/binfmt --install all   # arm64 etc. qemu handlers
```

Point the Windows `docker` CLI at it:

```powershell
[Environment]::SetEnvironmentVariable('DOCKER_HOST','tcp://127.0.0.1:2375','User')
$env:DOCKER_HOST='tcp://127.0.0.1:2375'   # for the current shell
docker version --format 'client {{.Client.Version}} / server {{.Server.Version}}'
```

## Step 5 — Export the kubeconfig to Windows

k3s writes `/etc/rancher/k3s/k3s.yaml` with server `https://127.0.0.1:6443` (works from Windows under
mirrored networking; its TLS cert lists `127.0.0.1`). Rename its `default` cluster/context/user to
**`erun-k3s`** and write it to `~/.kube/config`. The regex must handle **both** `  name: default`
**and** k3s's `- name: default` (users list) — a naïve `: default$`-only match renames the context's
user reference but not the user entry, breaking auth (`Please enter Username`):

```powershell
$raw = (wsl -d Ubuntu -u root cat /etc/rancher/k3s/k3s.yaml) -join "`n"
New-Item -ItemType Directory -Force "$env:USERPROFILE\.kube" | Out-Null
($raw -replace '(?m)^(\s*-?\s*(?:name|cluster|user|current-context):\s*)default\s*$', '${1}erun-k3s') |
  Set-Content -Encoding ascii "$env:USERPROFILE\.kube\config"
kubectl config use-context erun-k3s
kubectl get nodes            # Ready node reached from Windows == success
```

If `~/.kube/config` already has contexts, write the renamed file to a second path and merge with
`$env:KUBECONFIG="$HOME\.kube\config;$HOME\.kube\erun-k3s.yaml"; kubectl config view --flatten`.

## Step 6 — Wire an erun environment at the cluster

Run `erun init` **inside the project** you want to deploy (it initializes an environment for the
current project). It writes the registry to the project's `.erun/config.yaml` and the kube-context to
the user env config (`%LOCALAPPDATA%\erun\<tenant>\<env>\config.yaml`):

```powershell
cd <your-project>
erun init <tenant> local --type local-agent --kubernetes-context erun-k3s --container-registry localhost:5000 -y
```

Result: project `.erun/config.yaml` gets `environments.local.containerregistries: [{registry:
localhost:5000, roles: [build, deploy]}]`; the user env config gets `kubernetescontext: erun-k3s`
and `type: local-agent`.

**Prove the wiring** (deploy is a pure primitive — it needs an explicit `--version`):

```powershell
erun deploy <tenant> local --version <v> --dry-run
```

A correct plan shows `kubectl --context erun-k3s …` and
`helm upgrade --install … --kube-context erun-k3s … --set-string containerRegistry=localhost:5000 …
oci://localhost:5000/charts/erun-devops`. A real `erun deploy` first needs `erun build`/`erun push`
to publish the runtime image + chart to `localhost:5000`.

## Step 6b — Remote-agent envs: the context-resolved cluster registry

`--container-registry localhost:5000` (Step 6) is right for a **local-agent** env,
where `erun build` runs on the host and pushes over `localhost:5000`. A
**remote-agent** env builds **inside a pod**, whose `localhost` is its own loopback —
not the node's registry — so it needs the registry addressed by something a pod can
reach. Use a **cluster registry** entry, which erun resolves from the env's
kube-context: the cluster (and the in-pod build) use the registry's ClusterIP; a
host build gets an automatic `kubectl port-forward`.

```yaml
# .erun/config.yaml → environments.<env>.containerregistries
containerregistries:
  - cluster: { service: erun-registry, namespace: kube-system, port: 5000, insecure: true }
    roles: [build, deploy]        # in-pod build pushes here; cluster pulls here
```

- `deploy` resolves to the ClusterIP (`10.43.0.100:5000`) rendered into the chart; the
  node pulls it via the `registries.yaml` mirror (Step 3).
- `build` resolves to the ClusterIP directly for an in-pod build, or `localhost:<port>`
  via a managed port-forward on the host.
- `insecure: true` makes `erun deploy` pass `--insecure-registry <ClusterIP>:5000` to the
  in-pod dind daemon so the plain-HTTP push is accepted.

**Publish to a shared registry when done.** Add a `from`+`publish` (`to`) pair and run
`erun publish <tenant> <env> --version <v>` to mirror the exact tested image from the
cluster registry to `ghcr.io/<org>` without rebuilding or redeploying:

```yaml
containerregistries:
  - cluster: { service: erun-registry, namespace: kube-system, port: 5000, insecure: true }
    roles: [build, deploy, from]
  - registry: ghcr.io/<org>
    roles: [to]
```

## Step 7 — Durability across reboots

systemd keeps k3s (and the registry + dockerd) running while the distro is up, but a WSL2 distro only
runs while a session is open, and it **idle-shuts-down within ~60s of the last session ending** —
taking k3s with it, so Windows loses `127.0.0.1:6443` (the desktop app shows
`dial tcp 127.0.0.1:6443: connection refused`). A task that merely *boots* the distro (`--exec
/bin/true` returns instantly) does not prevent this. Register a logon task that **holds a persistent
session open** so the VM never idle-shuts-down:

```powershell
# sleep infinity holds one WSL session open for as long as the VM lives, which is
# what pins the VM (and k3s/registry/dockerd) up. Default user avoids the harmless
# "systemd user session for root" warning; the distro's systemd still autostarts
# the k3s *system* service on boot.
$action  = New-ScheduledTaskAction -Execute 'wsl.exe' -Argument '-d Ubuntu --exec sleep infinity'
$trigger = New-ScheduledTaskTrigger -AtLogOn
# Resilient settings are ESSENTIAL: Task Scheduler's defaults stop a task after the
# machine is idle (~10 min) and on battery — either kills the holder and the VM dies.
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
  -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew `
  -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -StartWhenAvailable
$settings.IdleSettings.StopOnIdleEnd = $false   # <- the critical one: do not stop when idle
$settings.IdleSettings.RestartOnIdle = $false
$principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName 'erun-k3s-boot' -Action $action -Trigger $trigger `
  -Settings $settings -Principal $principal -Force
Start-ScheduledTask -TaskName 'erun-k3s-boot'   # bring it up now (also runs at every logon)
```

After boot, k3s takes ~15–20s to be Ready; the first command after a cold start may need a moment.

> If `Start-ScheduledTask` / a scheduler-initiated trigger returns `0x80070005` (access denied) when
> starting the task on demand, use `schtasks /run /tn erun-k3s-boot` instead — the `LogonTrigger`
> itself fires correctly at real logon.

## Verify (full round-trip)

```powershell
$env:DOCKER_HOST='tcp://127.0.0.1:2375'; $env:NO_PROXY='127.0.0.1,localhost'
kubectl get nodes                                                    # Ready
docker pull -q hello-world; docker tag hello-world localhost:5000/hello:test
docker push localhost:5000/hello:test                               # Windows docker -> WSL daemon -> registry
kubectl run hello-erun --image=localhost:5000/hello:test --restart=Never
kubectl wait --for=jsonpath='{.status.phase}'=Succeeded pod/hello-erun --timeout=90s   # k3s pulled it
& "$env:SystemRoot\System32\curl.exe" -sS --noproxy '*' http://localhost:5000/v2/_catalog   # {"repositories":["hello"]}
kubectl delete pod hello-erun
```

A push from Windows that k3s then pulls into a running pod proves the exact build→push→pull path erun
uses.

## Maintenance, repair & teardown

- **Detect.** `kubectl --context erun-k3s get nodes` Ready → the cluster exists; reconcile in place.
- **Unreachable from Windows** (`dial tcp 127.0.0.1:6443: connection refused`) → first check the VM is
  even up: `wsl --list --running`. If Ubuntu is absent, the holder task died — the usual cause is
  `erun-k3s-boot` missing the resilient settings from Step 7 (stops on idle/battery) or still using a
  non-holding action (`--exec /bin/true`); re-register it per Step 7 and `Start-ScheduledTask
  erun-k3s-boot`. If the VM *is* up but still unreachable, re-check `.wslconfig` has both
  `networkingMode=mirrored` and `hostAddressLoopback=true`, then `wsl --shutdown` and reboot the distro.
- **Registry pulls fail** → re-apply `registries.yaml` + the hostNetwork registry manifest (Step 3),
  `wsl -d Ubuntu -u root systemctl restart k3s`.
- **Upgrade k3s** → re-run the Step 3 install line inside the distro.
- **Teardown** (destructive):
  ```powershell
  wsl -d Ubuntu -u root /usr/local/bin/k3s-uninstall.sh
  Unregister-ScheduledTask -TaskName 'erun-k3s-boot' -Confirm:$false
  # optional, removes the whole distro: wsl --unregister Ubuntu
  ```
  Point the operator at teardown; never run it as a maintenance side effect.

## Troubleshooting

- **`kubectl` → `error: EOF` then `Please enter Username`** → the kubeconfig's context references a
  user that doesn't exist because the rename missed k3s's `- name: default`; re-run Step 5's regex.
- **`kubectl`/registry unreachable but the distro is fine internally** → the distro cold-started and
  k3s isn't Ready yet, or `.wslconfig` is missing `hostAddressLoopback=true`.
- **`Invoke-WebRequest http://localhost:5000/...` times out** → a .NET HttpClient quirk over the WSL
  loopback; use `curl.exe --noproxy '*'` instead. `docker push`/`pull` over the same address work.
- **`iptables absent` / registry `:5000` not listening on the host** → the CNI portmap hostPort path
  needs iptables and is flaky on WSL2; this is why the registry runs on `hostNetwork` (Step 3) — don't
  switch it back to `hostPort`.
- **k3s won't start (containerd errors about `/dev/kmsg`)** → the `dev-kmsg.service` (Step 3) is
  missing or disabled.
- **A corporate proxy breaks localhost** → set `NO_PROXY=127.0.0.1,localhost` (kubectl/erun) or use
  `curl.exe --noproxy '*'`.

## Important

- Use **k3s**, never **k3d** — k3d is the erun test harness's disposable cluster (`erun-ui/playwright`).
- Keep `localhost:5000` (registry) and `erun-k3s` (context) as the fixed names; the whole wiring is
  consistent around them.
- No Docker Desktop: `dockerd` runs in the distro and the Windows `docker` CLI reaches it via
  `DOCKER_HOST`. This is a local, plain-HTTP, no-auth dev registry and an unauthenticated local
  Docker socket — keep both bound to `localhost`; never expose them off the machine.
</content>
