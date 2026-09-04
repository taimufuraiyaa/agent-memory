package objectcustody

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	graphProjectionBucket = "agent-memory-graph-projections"
	graphArtifactBucket   = "agent-memory-graph-artifacts"
)

// MinIOGraphObjects implements the common immutable custody capability used by
// the database-capable projection service and isolated graph worker. Bucket
// policy still determines which half each workload can access.
type MinIOGraphObjects struct{ client *minio.Client }

func NewMinIOGraphObjects(endpoint, accessKey, secretKey string) (*MinIOGraphObjects, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Scheme != "http" && parsed.Scheme != "https" || strings.TrimSpace(accessKey) == "" || strings.TrimSpace(secretKey) == "" {
		return nil, fmt.Errorf("invalid graph object storage configuration")
	}
	client, err := minio.New(parsed.Host, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: parsed.Scheme == "https"})
	if err != nil {
		return nil, err
	}
	return &MinIOGraphObjects{client: client}, nil
}

func (s *MinIOGraphObjects) PutImmutable(ctx context.Context, key string, value []byte, expires time.Time) error {
	bucket, object, err := graphObjectLocation(key)
	if err != nil || expires.IsZero() || !expires.After(time.Now().UTC().Add(-time.Minute)) {
		return fmt.Errorf("invalid graph object write")
	}
	options := minio.PutObjectOptions{ContentType: "application/octet-stream", UserMetadata: map[string]string{"agent-memory-expires-at": expires.UTC().Format(time.RFC3339Nano)}}
	options.SetMatchETagExcept("*")
	_, err = s.client.PutObject(ctx, bucket, object, bytes.NewReader(value), int64(len(value)), options)
	if err != nil {
		response := minio.ToErrorResponse(err)
		if response.StatusCode == 412 || response.Code == "PreconditionFailed" {
			return ErrGraphObjectAlreadyExists
		}
	}
	return err
}

func (s *MinIOGraphObjects) Get(ctx context.Context, key string) ([]byte, error) {
	bucket, object, err := graphObjectLocation(key)
	if err != nil {
		return nil, err
	}
	value, err := s.client.GetObject(ctx, bucket, object, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer value.Close()
	contents, err := io.ReadAll(io.LimitReader(value, (20<<30)+1))
	if err != nil {
		return nil, err
	}
	if len(contents) > 20<<30 {
		return nil, fmt.Errorf("graph object exceeds policy")
	}
	return contents, nil
}

func (s *MinIOGraphObjects) Delete(ctx context.Context, key string) error {
	bucket, object, err := graphObjectLocation(key)
	if err != nil {
		return err
	}
	return s.client.RemoveObject(ctx, bucket, object, minio.RemoveObjectOptions{})
}

func (s *MinIOGraphObjects) DeletePrefix(ctx context.Context, prefix string) error {
	bucket, objectPrefix, err := graphObjectLocation(prefix)
	if err != nil || !strings.HasSuffix(prefix, "/") {
		return fmt.Errorf("invalid graph object cleanup prefix")
	}
	var failures []error
	for object := range s.client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: objectPrefix, Recursive: true}) {
		if object.Err != nil {
			failures = append(failures, object.Err)
			continue
		}
		if err := s.client.RemoveObject(ctx, bucket, object.Key, minio.RemoveObjectOptions{}); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func graphObjectLocation(key string) (string, string, error) {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") || path.Clean(key) != strings.TrimSuffix(key, "/") && path.Clean(key)+"/" != key || strings.Contains(key, "..") {
		return "", "", fmt.Errorf("invalid graph object key")
	}
	switch {
	case strings.HasPrefix(key, "graph-projections/"):
		return graphProjectionBucket, key, nil
	case strings.HasPrefix(key, "graph-artifacts/"):
		return graphArtifactBucket, key, nil
	default:
		return "", "", fmt.Errorf("graph object key is outside custody")
	}
}
