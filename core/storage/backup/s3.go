package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// S3Storage works against any S3-compatible endpoint — AWS S3, Cloudflare R2,
// Hetzner Object Storage, Backblaze B2, MinIO, Wasabi.
type S3Storage struct {
	client *s3.Client
	bucket string
}

// S3Config matches the JSONB blob in the backup_storages.config column.
type S3Config struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	ForcePathStyle  bool   `json:"forcePathStyle"`
}

func NewS3(ctx context.Context, raw json.RawMessage) (*S3Storage, error) {
	var cfg S3Config
	if len(raw) == 0 {
		return nil, fmt.Errorf("s3 storage requires config")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("invalid s3 config: %w", err)
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3 storage requires bucket")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("s3 storage requires access key + secret")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1" // safe default for many providers
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.ForcePathStyle
	})

	return &S3Storage{client: client, bucket: cfg.Bucket}, nil
}

func (s *S3Storage) Provider() string { return "s3" }

func (s *S3Storage) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   r,
	}
	if size > 0 {
		input.ContentLength = aws.Int64(size)
	}
	_, err := s.client.PutObject(ctx, input)
	return err
}

func (s *S3Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

func (s *S3Storage) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// Treat missing object as a no-op (idempotent delete).
		var ae smithy.APIError
		if errors.As(err, &ae) && (ae.ErrorCode() == "NoSuchKey" || ae.ErrorCode() == "NotFound") {
			return nil
		}
	}
	return err
}

func (s *S3Storage) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			out = append(out, Object{
				Key:          aws.ToString(obj.Key),
				Size:         aws.ToInt64(obj.Size),
				LastModified: aws.ToTime(obj.LastModified),
			})
		}
	}
	return out, nil
}

func (s *S3Storage) Stat(ctx context.Context, key string) (Object, error) {
	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return Object{}, err
	}
	_ = types.ObjectStorageClassStandard // pull in types package
	return Object{
		Key:          key,
		Size:         aws.ToInt64(head.ContentLength),
		LastModified: aws.ToTime(head.LastModified),
	}, nil
}

func (s *S3Storage) DownloadURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = time.Hour
	}
	presigner := s3.NewPresignClient(s.client)
	out, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return out.URL, nil
}
