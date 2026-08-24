package export

import (
	"bytes"
	"context"
	"errors"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"io"
	"net/url"
)

const exportBucket = "agent-memory-exports"

type MinIOStore struct{ client *minio.Client }

func NewMinIOStore(endpoint, accessKey, secretKey string) (*MinIOStore, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return nil, errors.New("invalid object endpoint")
	}
	client, err := minio.New(parsed.Host, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: parsed.Scheme == "https"})
	if err != nil {
		return nil, err
	}
	return &MinIOStore{client: client}, nil
}
func (s *MinIOStore) Put(ctx context.Context, key string, value []byte) error {
	_, err := s.client.PutObject(ctx, exportBucket, key, bytes.NewReader(value), int64(len(value)), minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}
func (s *MinIOStore) Get(ctx context.Context, key string) ([]byte, error) {
	object, err := s.client.GetObject(ctx, exportBucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer object.Close()
	return io.ReadAll(object)
}
