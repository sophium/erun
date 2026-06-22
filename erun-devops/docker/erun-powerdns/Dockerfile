# Passthrough wrapper over the upstream PowerDNS Authoritative Server image,
# following the wrap-and-pin pattern (erun-devops/AGENTS.md). The pinned
# version lives in the sibling VERSION file and in the chart's default tag;
# they must move together. The 4.9 line ships as repo `pdns-auth-49`.
ARG POWERDNS_VERSION=4.9.3
FROM powerdns/pdns-auth-49:${POWERDNS_VERSION}
