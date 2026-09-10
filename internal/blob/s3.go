// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config points at an S3-compatible object store.
//
// Ceph's RADOS Gateway is the target this was written for: it speaks the S3 API,
// replicates and scrubs the objects itself, and grows by adding OSDs, which is
// exactly the shape of the frame workload — write once, read seldom, never
// mutate, delete in bulk when a project goes. MinIO and AWS S3 work through the
// same client with no code change; only the endpoint differs.
type S3Config struct {
	// Endpoint is host:port without a scheme, e.g. `ceph-rgw:8080`.
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	// Region is optional; Ceph ignores it unless the zonegroup is configured
	// with one, but the signature includes it, so it has to match when it is.
	Region string
	UseSSL bool
}

// S3 stores frames as objects. Reads are streamed straight through to the HTTP
// response rather than handed out as presigned URLs, because the frame endpoint
// is authorised per project — a presigned URL would hand out access that
// outlives the check.
type S3 struct {
	client *minio.Client
	bucket string
}

func NewS3(ctx context.Context, cfg S3Config) (*S3, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("blob: S3 needs an endpoint and a bucket")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("blob: s3 client: %w", err)
	}
	// Create the bucket if it is missing so a fresh Ceph cluster needs no manual
	// setup step. An existing bucket, or one another replica just created, is
	// not an error.
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("blob: reaching %s: %w", cfg.Endpoint, err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			if exists, e2 := client.BucketExists(ctx, cfg.Bucket); e2 != nil || !exists {
				return nil, fmt.Errorf("blob: creating bucket %s: %w", cfg.Bucket, err)
			}
		}
	}
	return &S3{client: client, bucket: cfg.Bucket}, nil
}

func (s *S3) Kind() string { return "s3" }
func (s *S3) Close() error { return nil }

func (s *S3) Put(ctx context.Context, key string, data []byte) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "image/png"})
	return err
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	// GetObject is lazy: it does not talk to the server until the first read or
	// Stat, so a missing key only surfaces here.
	st, err := obj.Stat()
	if err != nil {
		obj.Close()
		if minio.ToErrorResponse(err).StatusCode == 404 {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	return obj, st.Size, nil
}

// DeletePrefix removes every object of one session. The listing and the removal
// are streamed so a long recording does not have to fit in memory.
func (s *S3) DeletePrefix(ctx context.Context, prefix string) error {
	objects := s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix: prefix, Recursive: true,
	})
	var firstErr error
	for e := range s.client.RemoveObjects(ctx, s.bucket, objects, minio.RemoveObjectsOptions{}) {
		if e.Err != nil && firstErr == nil {
			firstErr = fmt.Errorf("blob: removing %s: %w", e.ObjectName, e.Err)
		}
	}
	return firstErr
}

// Walk iterates over every object under the prefix.
func (s *S3) Walk(ctx context.Context, prefix string, fn func(key string, size int64) error) error {
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix: prefix, Recursive: true,
	}) {
		if obj.Err != nil {
			return obj.Err
		}
		if err := fn(obj.Key, obj.Size); err != nil {
			return err
		}
	}
	return nil
}
