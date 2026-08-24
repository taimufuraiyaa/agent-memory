package audit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const archiveBucket = "agent-memory-audit"

type MinIOArchiveStore struct {
	client    *minio.Client
	retention time.Duration
}

func NewMinIOArchiveStore(endpoint, accessKey, secretKey string, retention time.Duration) (*MinIOArchiveStore, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || retention <= 0 {
		return nil, errors.New("invalid audit archive configuration")
	}
	client, err := minio.New(parsed.Host, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: parsed.Scheme == "https"})
	if err != nil {
		return nil, err
	}
	return &MinIOArchiveStore{client: client, retention: retention}, nil
}

func (s *MinIOArchiveStore) PutImmutable(ctx context.Context, key string, value []byte, checksum string) error {
	if s == nil || s.client == nil || !strings.HasPrefix(key, "audit/") || checksum == "" {
		return errors.New("invalid audit archive object")
	}
	if _, err := s.client.StatObject(ctx, archiveBucket, key, minio.StatObjectOptions{}); err == nil {
		return errors.New("immutable audit archive object exists")
	} else if code := minio.ToErrorResponse(err).Code; code != "NoSuchKey" && code != "NoSuchObject" && code != "NotFound" {
		return err
	}
	_, err := s.client.PutObject(ctx, archiveBucket, key, bytes.NewReader(value), int64(len(value)), minio.PutObjectOptions{
		ContentType: "application/json", UserMetadata: map[string]string{"sha256": checksum},
		Mode: minio.Compliance, RetainUntilDate: time.Now().UTC().Add(s.retention),
	})
	return err
}

func (s *MinIOArchiveStore) Get(ctx context.Context, key string) ([]byte, error) {
	if s == nil || s.client == nil || !strings.HasPrefix(key, "audit/") {
		return nil, errors.New("invalid audit archive object")
	}
	object, err := s.client.GetObject(ctx, archiveBucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer object.Close()
	return io.ReadAll(object)
}
