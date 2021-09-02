// Program notes-backup backs up notes database to S3
package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"golang.org/x/sync/errgroup"
	_ "modernc.org/sqlite"
)

func main() {
	log.SetFlags(0)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	args := runArgs{}
	flag.StringVar(&args.Bucket, "bucket", args.Bucket, "s3 bucket name")
	flag.StringVar(&args.DB, "db", args.DB, "`path` to sqlite database")
	flag.Parse()
	if err := run(ctx, args); err != nil {
		log.Fatal(err)
	}
}

type runArgs struct {
	DB, Bucket string
}

func run(ctx context.Context, args runArgs) error {
	if args.DB == "" {
		return errors.New("no database provided")
	}
	if args.Bucket == "" {
		return errors.New("no bucket name provided")
	}
	st, err := os.Stat(args.DB)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("not a regular file")
	}

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return err
	}
	s3client := s3.NewFromConfig(cfg)
	uploader := manager.NewUploader(s3client)

	tmpDir, err := os.MkdirTemp("", "sqlite-backup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	db, err := sql.Open("sqlite", args.DB)
	if err != nil {
		return err
	}
	defer db.Close()
	backupFile := filepath.Join(tmpDir, "backup.sqlite")
	_, err = db.ExecContext(ctx, `VACUUM INTO ?`, backupFile)
	if err != nil {
		return err
	}
	db.Close()
	r, w := io.Pipe()
	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error {
		defer w.Close()
		f, err := os.Open(backupFile)
		if err != nil {
			return err
		}
		defer f.Close()
		gw := gzip.NewWriter(w)
		if _, err := io.Copy(gw, f); err != nil {
			return err
		}
		return w.CloseWithError(gw.Close())
	})
	group.Go(func() error {
		defer r.Close()
		year, week := time.Now().ISOWeek()
		objectKey := fmt.Sprintf("%d-%d-%s.gz", year, week, filepath.Base(args.DB))
		_, err := uploader.Upload(ctx, &s3.PutObjectInput{
			Bucket:       &args.Bucket,
			Key:          &objectKey,
			Body:         r,
			ACL:          types.ObjectCannedACLPrivate,
			StorageClass: types.StorageClassStandardIa,
		})
		return err
	})
	return group.Wait()
}
