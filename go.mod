module github.com/witlox/ghyll

go 1.22

// Dependencies will be added during implementation:
// - github.com/BurntSushi/toml (config parsing)
// - github.com/mattn/go-sqlite3 (checkpoint store)
// - github.com/yalue/onnxruntime_go (embedding model)
// - golang.org/x/crypto (ed25519 — though stdlib crypto/ed25519 may suffice)

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/cucumber/gherkin/go/v26 v26.2.0 // indirect
	github.com/cucumber/godog v0.15.1 // indirect
	github.com/cucumber/messages/go/v21 v21.0.1 // indirect
	github.com/gofrs/uuid v4.3.1+incompatible // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-memdb v1.3.4 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/spf13/pflag v1.0.7 // indirect
)
