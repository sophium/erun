ARG ERUN_BASE_TAG=erunpaas/erun-devops:1.0.0

FROM ${ERUN_BASE_TAG}

ENTRYPOINT ["/bin/sh", "-lc", "if [ \"${1:-}\" = shell ]; then shift; repo_dir=\"${ERUN_REPO_PATH:-${HOME}/git/erun}\"; [ -d \"$repo_dir\" ] && cd \"$repo_dir\"; exec /bin/bash -i; fi; exec erun-devops-entrypoint \"$@\"", "erun-devops-wrapper"]
