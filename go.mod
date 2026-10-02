module github.com/grodier/rss

go 1.26.5

require (
	github.com/alexedwards/scs/postgresstore v0.0.0-20251002162104-209de6e426de
	github.com/alexedwards/scs/v2 v2.9.0
	github.com/go-chi/chi/v5 v5.3.1
	github.com/go-playground/form/v4 v4.3.0
	github.com/lib/pq v1.12.3
)

require golang.org/x/crypto v0.56.0

require (
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.35.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/tools v0.44.1-0.20260420230617-19499e7caabc // indirect
	honnef.co/go/tools v0.8.1 // indirect
)

tool honnef.co/go/tools/cmd/staticcheck
