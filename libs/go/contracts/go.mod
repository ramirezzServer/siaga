module github.com/ramirezzServer/siaga/libs/go/contracts

go 1.26.0

// protoc-gen-go dijalankan lewat `go tool` agar versinya sama dengan runtime protobuf.
tool google.golang.org/protobuf/cmd/protoc-gen-go

require google.golang.org/protobuf v1.36.10 // indirect
