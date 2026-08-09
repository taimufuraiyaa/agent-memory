package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type config struct {
	endpoint  string
	accessKey string
	secretKey string
	secure    bool
}

type bucketClient interface {
	BucketExists(context.Context, string) (bool, error)
	MakeBucket(context.Context, string, minio.MakeBucketOptions) error
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "health" {
		endpoint := "http://localhost:4566/_localstack/health"
		if len(os.Args) > 2 {
			endpoint = os.Args[2]
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := probeHealth(ctx, endpoint, &http.Client{Timeout: 2 * time.Second}); err != nil {
			log.Fatal(err)
		}
		return
	}
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	parsed, _ := url.Parse(cfg.endpoint)
	client, err := minio.New(parsed.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.accessKey, cfg.secretKey, ""),
		Secure: cfg.secure,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := ensureBuckets(context.Background(), client); err != nil {
		log.Fatal(err)
	}
	fmt.Println("S3 buckets initialized")
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func probeHealth(ctx context.Context, endpoint string, client httpDoer) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" {
		return errors.New("health endpoint must be local HTTP")
	}
	if client == nil {
		return errors.New("health client is required")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %d", response.StatusCode)
	}
	var status struct {
		Services map[string]string `json:"services"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&status); err != nil {
		return fmt.Errorf("decode health response: %w", err)
	}
	if status.Services["s3"] != "running" {
		return errors.New("Floci S3 service is not running")
	}
	return nil
}

func loadConfig(getenv func(string) string) (config, error) {
	if getenv == nil {
		return config{}, errors.New("environment reader is required")
	}
	endpoint := strings.TrimSpace(getenv("AGENT_MEMORY_OBJECT_ENDPOINT"))
	accessKey := strings.TrimSpace(getenv("AGENT_MEMORY_OBJECT_ACCESS_KEY"))
	secretKey := strings.TrimSpace(getenv("AGENT_MEMORY_OBJECT_SECRET_KEY"))
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return config{}, errors.New("AGENT_MEMORY_OBJECT_ENDPOINT must be an HTTP or HTTPS endpoint")
	}
	if accessKey == "" || secretKey == "" {
		return config{}, errors.New("object-store credentials are required")
	}
	return config{endpoint: endpoint, accessKey: accessKey, secretKey: secretKey, secure: parsed.Scheme == "https"}, nil
}

func ensureBuckets(ctx context.Context, client bucketClient) error {
	if client == nil {
		return errors.New("bucket client is required")
	}
	buckets := []struct {
		name       string
		objectLock bool
	}{
		{name: "agent-memory-quarantine"},
		{name: "agent-memory-vault"},
		{name: "agent-memory-exports"},
		{name: "agent-memory-audit", objectLock: true},
	}
	for _, bucket := range buckets {
		exists, err := client.BucketExists(ctx, bucket.name)
		if err != nil {
			return fmt.Errorf("check bucket %s: %w", bucket.name, err)
		}
		if exists {
			continue
		}
		if err := client.MakeBucket(ctx, bucket.name, minio.MakeBucketOptions{ObjectLocking: bucket.objectLock}); err != nil {
			return fmt.Errorf("create bucket %s: %w", bucket.name, err)
		}
	}
	return nil
}
