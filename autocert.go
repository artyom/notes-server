package main

import (
	"context"
	"database/sql"

	"golang.org/x/crypto/acme/autocert"
)

func knownAcmeDomain(db *sql.DB) (string, error) {
	var domain string
	err := db.QueryRow(`select Key from autocert where key not like ? limit 1`, "acme_account%").Scan(&domain)
	return domain, err
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
