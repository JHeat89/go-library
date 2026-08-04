// Package aws adapts AWS Secrets Manager and SSM Parameter Store to
// secrets.Source. Only this package (within the module) imports the AWS
// SDK, so consumers who don't load secrets from AWS never pull it in.
//
// The consumer builds and owns the aws.Config — this package does not import
// aws-sdk-go-v2/config — matching the consumer-owns-the-client precedent set
// by oauth/redis and graphql/gqlgen.
//
//	cfg, err := config.LoadDefaultConfig(ctx)
//	src := aws.New(cfg)
//	loader := secrets.New(log, src)
package aws

import (
	"context"
	"errors"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/JHeat89/go-library/secrets"
)

// secretsManagerAPI is the narrow slice of the Secrets Manager client this
// package needs, so tests can supply a plain fake instead of the SDK's test
// machinery.
type secretsManagerAPI interface {
	GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// ssmAPI is the narrow slice of the SSM client this package needs.
type ssmAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	GetParametersByPath(ctx context.Context, in *ssm.GetParametersByPathInput, opts ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
}

// Source adapts AWS Secrets Manager and SSM Parameter Store to
// secrets.Source.
type Source struct {
	sm  secretsManagerAPI
	ssm ssmAPI
}

var _ secrets.Source = (*Source)(nil)

// New builds a Source backed by Secrets Manager and SSM clients constructed
// from cfg.
func New(cfg awssdk.Config) *Source {
	return &Source{
		sm:  secretsmanager.NewFromConfig(cfg),
		ssm: ssm.NewFromConfig(cfg),
	}
}

// Secret fetches name from Secrets Manager, preferring SecretString and
// falling back to SecretBinary. A ResourceNotFoundException is translated to
// secrets.ErrNotFound.
func (s *Source) Secret(ctx context.Context, name string) (string, error) {
	out, err := s.sm.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: awssdk.String(name),
	})
	if err != nil {
		var notFound *smtypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return "", secrets.ErrNotFound
		}
		return "", err
	}

	if out.SecretString != nil {
		return *out.SecretString, nil
	}
	return string(out.SecretBinary), nil
}

// Parameter fetches the decrypted value of name from SSM Parameter Store. A
// ParameterNotFound error is translated to secrets.ErrNotFound.
func (s *Source) Parameter(ctx context.Context, name string) (string, error) {
	out, err := s.ssm.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(name),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			return "", secrets.ErrNotFound
		}
		return "", err
	}

	return awssdk.ToString(out.Parameter.Value), nil
}

// ParametersByPath fetches every decrypted parameter under path, recursively,
// following NextToken pagination until exhausted. The result is keyed by
// each parameter's full name.
func (s *Source) ParametersByPath(ctx context.Context, path string) (map[string]string, error) {
	result := make(map[string]string)

	var nextToken *string
	for {
		out, err := s.ssm.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
			Path:           awssdk.String(path),
			Recursive:      awssdk.Bool(true),
			WithDecryption: awssdk.Bool(true),
			NextToken:      nextToken,
		})
		if err != nil {
			var notFound *ssmtypes.ParameterNotFound
			if errors.As(err, &notFound) {
				return nil, secrets.ErrNotFound
			}
			return nil, err
		}

		for _, p := range out.Parameters {
			result[awssdk.ToString(p.Name)] = awssdk.ToString(p.Value)
		}

		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	return result, nil
}
