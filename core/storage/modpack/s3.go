package modpack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// S3Provider stores .mrpack files in a single S3-compatible bucket. Works
// against AWS, Cloudflare R2, Backblaze B2, MinIO, etc.
type S3Provider struct {
	client *s3.Client
	bucket string
}

// NewS3 builds an S3-backed modpack provider. Region defaults to us-east-1
// when empty (safe default for most providers).
func NewS3(endpoint, region, bucket, accessKey, secretKey string) (*S3Provider, error) {
	if bucket == "" {
		return nil, fmt.Errorf("modpack storage: s3 bucket required")
	}
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("modpack storage: s3 access key + secret required")
	}
	if region == "" {
		region = "us-east-1"
	}

	ctx := context.Background()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("modpack storage: s3 config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})

	return &S3Provider{client: client, bucket: bucket}, nil
}

func (s *S3Provider) Put(key string, data []byte) error {
	_, err := s.client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
	})
	if err != nil {
		return fmt.Errorf("modpack storage: s3 put %s: %w", key, err)
	}
	return nil
}

func (s *S3Provider) Get(key string) ([]byte, error) {
	out, err := s.client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("modpack storage: s3 get %s: %w", key, err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("modpack storage: s3 read %s: %w", key, err)
	}
	return data, nil
}

func (s *S3Provider) Delete(key string) error {
	_, err := s.client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil
		}
		return fmt.Errorf("modpack storage: s3 delete %s: %w", key, err)
	}
	return nil
}

func (s *S3Provider) Stat(key string) (int64, bool, error) {
	head, err := s.client.HeadObject(context.Background(), &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("modpack storage: s3 head %s: %w", key, err)
	}
	return aws.ToInt64(head.ContentLength), true, nil
}

// isS3NotFound reports whether an AWS API error indicates a missing object.
// HeadObject returns "NotFound"; GetObject / DeleteObject return "NoSuchKey".
func isS3NotFound(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		code := ae.ErrorCode()
		return code == "NoSuchKey" || code == "NotFound"
	}
	return false
}
