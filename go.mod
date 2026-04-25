module github.com/dh-kam/claude-creds-share

go 1.24.0

toolchain go1.24.4

require (
	// github.com/dh-kam/refutils — added in Phase 1 (cmd) with the proper
	// resolved version. Left out here so earlier phases can `go mod tidy`.
	github.com/fsnotify/fsnotify v1.7.0
	github.com/gin-gonic/gin v1.10.0
	github.com/gofrs/flock v0.13.0
	github.com/gorilla/websocket v1.5.3
	github.com/spf13/cobra v1.8.1
	github.com/spf13/viper v1.19.0
	github.com/stretchr/testify v1.11.1
	gopkg.in/yaml.v3 v3.0.1
)

require golang.org/x/sys v0.37.0 // indirect
