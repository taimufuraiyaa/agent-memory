package source

import (
	"bytes"
	"context"
	"errors"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"io"
	"net/url"
	"strings"
)

const quarantineBucket = "agent-memory-quarantine"
const vaultBucket = "agent-memory-vault"

var ErrVaultObjectExists = errors.New("immutable vault object already exists")

type MinIOQuarantine struct{ client *minio.Client }

func NewMinIOQuarantine(endpoint, accessKey, secretKey string) (*MinIOQuarantine, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return nil, errors.New("invalid object endpoint")
	}
	client, err := minio.New(parsed.Host, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: parsed.Scheme == "https"})
	if err != nil {
		return nil, err
	}
	return &MinIOQuarantine{client: client}, nil
}
func (s *MinIOQuarantine) Put(ctx context.Context, key string, body io.Reader, size int64, mediaType string) error {
	_, err := s.client.PutObject(ctx, quarantineBucket, key, body, size, minio.PutObjectOptions{ContentType: mediaType})
	return err
}
func (s *MinIOQuarantine) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, quarantineBucket, key, minio.RemoveObjectOptions{})
}
func (s *MinIOQuarantine) Get(ctx context.Context, key string) ([]byte, error) {
	object, err := s.client.GetObject(ctx, quarantineBucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer object.Close()
	return io.ReadAll(object)
}
func (s *MinIOQuarantine) PutVault(ctx context.Context, key string, value []byte) error {
	if _, err := s.client.StatObject(ctx, vaultBucket, key, minio.StatObjectOptions{}); err == nil {
		return ErrVaultObjectExists
	} else {
		code := minio.ToErrorResponse(err).Code
		if code != "NoSuchKey" && code != "NoSuchObject" && code != "NotFound" {
			return err
		}
	}
	_, err := s.client.PutObject(ctx, vaultBucket, key, bytes.NewReader(value), int64(len(value)), minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}
func (s *MinIOQuarantine) DeleteVault(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, vaultBucket, key, minio.RemoveObjectOptions{})
}
func (s *MinIOQuarantine) GetVault(ctx context.Context, tenantID, key string) ([]byte, error) {
	if err := validateVaultCapability(tenantID, key); err != nil {
		return nil, err
	}
	object, err := s.client.GetObject(ctx, vaultBucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer object.Close()
	return io.ReadAll(object)
}
func validateVaultCapability(tenantID, key string) error {
	prefix := "vault/" + tenantID + "/"
	if tenantID == "" || !strings.HasPrefix(key, prefix) {
		return errors.New("vault object is outside tenant capability")
	}
	return nil
}
func (s *MinIOQuarantine) ListVault(ctx context.Context, prefix string) ([]string, error) {
	result := []string{}
	for object := range s.client.ListObjects(ctx, vaultBucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if object.Err != nil {
			return nil, object.Err
		}
		result = append(result, object.Key)
	}
	return result, nil
}
