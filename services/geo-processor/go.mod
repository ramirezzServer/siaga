module github.com/ramirezzServer/siaga/services/geo-processor

go 1.24.0

// goose dijalankan lewat `go tool goose` agar versinya terkunci bersama kode.
tool github.com/pressly/goose/v3/cmd/goose

require (
	github.com/jackc/pgx/v5 v5.11.0
	github.com/pressly/goose/v3 v3.28.0
	github.com/ramirezzServer/siaga/libs/go/platform v0.0.0-00010101000000-000000000000
)

replace github.com/ramirezzServer/siaga/libs/go/platform => ../../libs/go/platform
