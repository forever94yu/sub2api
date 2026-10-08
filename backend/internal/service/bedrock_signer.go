package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// BedrockSigner 使用 AWS SigV4 对 Bedrock 请求签名
type BedrockSigner struct {
	credentials aws.Credentials
	region      string
	signer      *v4.Signer
}

// NewBedrockSigner 创建 BedrockSigner
func NewBedrockSigner(accessKeyID, secretAccessKey, sessionToken, region string) *BedrockSigner {
	return &BedrockSigner{
		credentials: aws.Credentials{
			AccessKeyID:     accessKeyID,
			SecretAccessKey: secretAccessKey,
			SessionToken:    sessionToken,
		},
		region: region,
		signer: v4.NewSigner(),
	}
}

// NewBedrockSignerFromAccount 从 Account 凭证创建 BedrockSigner
func NewBedrockSignerFromAccount(account *Account) (*BedrockSigner, error) {
	accessKeyID := account.GetCredential("aws_access_key_id")
	if accessKeyID == "" {
		return nil, fmt.Errorf("aws_access_key_id not found in credentials")
	}
	secretAccessKey := account.GetCredential("aws_secret_access_key")
	if secretAccessKey == "" {
		return nil, fmt.Errorf("aws_secret_access_key not found in credentials")
	}
	region := account.GetCredential("aws_region")
	if region == "" {
		region = defaultBedrockRegion
	}
	sessionToken := account.GetCredential("aws_session_token") // 可选

	return NewBedrockSigner(accessKeyID, secretAccessKey, sessionToken, region), nil
}

// SignRequest signs an InvokeModel request after its headers and body are final.
func (s *BedrockSigner) SignRequest(ctx context.Context, req *http.Request, body []byte) error {
	return s.signRequest(ctx, req, body, "bedrock")
}

func (s *BedrockSigner) signRequest(ctx context.Context, req *http.Request, body []byte, service string) error {
	payloadHash := sha256Hash(body)
	return s.signer.SignHTTP(ctx, s.credentials, req, payloadHash, service, s.region, time.Now())
}

func sha256Hash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
