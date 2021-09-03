package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"golang.org/x/crypto/acme/autocert"
	_ "modernc.org/sqlite"
)

func main() {
	log.SetFlags(0)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	args := runArgs{}
	flag.StringVar(&args.domain, "domain", args.domain, "certificate domain")
	flag.StringVar(&args.email, "email", args.email, "email to use for certificate-related notifications")
	flag.StringVar(&args.addr, "addr", args.addr, "address to listen")
	flag.StringVar(&args.database, "db", args.database, "`path` to the database")
	flag.Parse()
	if err := run(ctx, args); err != nil {
		log.Fatal(err)
	}
}

type runArgs struct {
	domain, addr, email, database string
}

func run(ctx context.Context, args runArgs) error {
	if args.email == "" {
		return errors.New("email cannot be empty")
	}
	if args.domain == "" {
		return errors.New("domain cannot be empty")
	}
	if args.addr == "" {
		return errors.New("address cannot be empty")
	}
	if args.database == "" {
		return errors.New("database must be set")
	}
	if st, err := os.Stat(args.database); err != nil {
		return err
	} else if !st.Mode().IsRegular() {
		return errors.New("database must be a regular file")
	}
	db, err := sql.Open("sqlite", args.database)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := initSchema(ctx, db); err != nil {
		return err
	}
	m := autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(args.domain),
		Cache:      newAutocertCache(db),
		Email:      args.email,
	}
	srv := &http.Server{
		Handler:           http.NewServeMux(),
		Addr:              args.addr,
		TLSConfig:         m.TLSConfig(),
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: time.Second,
		WriteTimeout:      time.Second,
	}
	go func() { <-ctx.Done(); srv.Close() }()
	return srv.ListenAndServeTLS("", "")
}

func newAutocertCache(db *sql.DB) *autocertCache {
	return &autocertCache{
		getSt: mustPrepare(db, `SELECT Value from autocert WHERE Key=@key`),
		putSt: mustPrepare(db, `INSERT OR REPLACE INTO autocert(Key,Value) VALUES(@key,@value)`),
		delSt: mustPrepare(db, `DELETE FROM autocert WHERE Key=@key`),
	}
}

type autocertCache struct {
	getSt *sql.Stmt
	putSt *sql.Stmt
	delSt *sql.Stmt
}

func (c *autocertCache) Get(ctx context.Context, key string) ([]byte, error) {
	var val []byte
	err := c.getSt.QueryRowContext(ctx, sql.Named("key", key)).Scan(&val)
	if err == sql.ErrNoRows {
		return nil, autocert.ErrCacheMiss
	}
	if err != nil {
		return nil, err
	}
	return val, nil
}

func (c *autocertCache) Put(ctx context.Context, key string, data []byte) error {
	_, err := c.putSt.ExecContext(ctx, sql.Named("key", key), sql.Named("value", data))
	return err
}

func (c *autocertCache) Delete(ctx context.Context, key string) error {
	_, err := c.delSt.ExecContext(ctx, sql.Named("key", key))
	return err
}

func initSchema(ctx context.Context, db *sql.DB) error {
	for _, s := range [...]string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=normal`,
		`PRAGMA busy_timeout=1000`,
		`CREATE TABLE IF NOT EXISTS autocert(
			Key TEXT PRIMARY KEY NOT NULL,
			Value BLOB NOT NULL
		)`,
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("SQL statement %q: %w", s, err)
		}
	}
	return nil
}

func mustPrepare(db *sql.DB, statement string) *sql.Stmt {
	st, err := db.Prepare(statement)
	if err != nil {
		panic(err)
	}
	return st
}
