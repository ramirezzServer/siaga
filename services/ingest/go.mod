module github.com/ramirezzServer/siaga/services/ingest

go 1.26.0

require (
	github.com/nats-io/nats.go v1.54.0
	github.com/ramirezzServer/siaga/libs/go/contracts v0.0.0-00010101000000-000000000000
	github.com/ramirezzServer/siaga/libs/go/platform v0.0.0-00010101000000-000000000000
	google.golang.org/protobuf v1.36.10
)

require (
	github.com/antithesishq/antithesis-sdk-go v0.8.0-default-no-op // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/minio/highwayhash v1.0.4 // indirect
	github.com/nats-io/jwt/v2 v2.8.2 // indirect
	github.com/nats-io/nats-server/v2 v2.15.0 // indirect
	github.com/nats-io/nkeys v0.4.16 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/time v0.16.0 // indirect
)

replace (
	github.com/ramirezzServer/siaga/libs/go/contracts => ../../libs/go/contracts
	github.com/ramirezzServer/siaga/libs/go/platform => ../../libs/go/platform
)
