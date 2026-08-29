module github.com/sophium/erun/erun-mcp

go 1.26.0

require (
	github.com/adrg/xdg v0.5.3
	github.com/go-jose/go-jose/v4 v4.1.4
	github.com/google/jsonschema-go v0.4.2
	github.com/modelcontextprotocol/go-sdk v1.4.1
	github.com/prometheus/client_golang v1.24.1
	github.com/prometheus/client_model v0.6.2
	github.com/prometheus/common v0.70.1
	github.com/sophium/erun/erun-common v0.0.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/coreos/go-oidc/v3 v3.18.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/sophium/erun/erun-common => ../erun-common
