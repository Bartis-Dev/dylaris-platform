module github.com/Bartis-Dev/dylaris-log-shipper

go 1.26.1

require github.com/redis/go-redis/v9 v9.18.0

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	go.uber.org/atomic v1.11.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

require dylaris-pkg v0.0.0

replace dylaris-pkg => ../pkg
