// ControlFinan in Go: same screens, rules and database as the Rails 8 + Hotwire app.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igorcb/controlfinan-go/app"
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func main() {
	ctx := context.Background()
	config, err := pgxpool.ParseConfig(env("DATABASE_URL", "postgres://app:app@localhost:5432/controlfinan_go"))
	if err != nil {
		log.Fatal(err)
	}
	poolSize, _ := strconv.Atoi(env("DB_POOL", "10"))
	config.MaxConns = int32(poolSize)
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := app.Migrate(ctx, db); err != nil {
		log.Fatal(err)
	}

	location, err := time.LoadLocation(env("APP_TIMEZONE", "America/Sao_Paulo"))
	if err != nil {
		log.Fatal(err)
	}
	secret := os.Getenv("SECRET_KEY_BASE")
	if secret == "" {
		log.Fatal("SECRET_KEY_BASE is required")
	}
	cost, _ := strconv.Atoi(env("BCRYPT_COST", "12"))
	server, err := app.New(app.Config{DB: db, Secret: secret, BcryptCost: cost, Location: location})
	if err != nil {
		log.Fatal(err)
	}

	address := ":" + env("PORT", "8000")
	log.Printf("ControlFinan listening on %s", address)
	log.Fatal(http.ListenAndServe(address, server))
}
