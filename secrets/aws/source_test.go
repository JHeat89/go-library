package aws

import (
	"context"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/JHeat89/go-library/secrets"
)

// fakeSecretsManager and fakeSSM implement the package's narrow client
// interfaces directly, with plain stdlib fakes — no SDK mocking library.

type fakeSecretsManager struct {
	out *secretsmanager.GetSecretValueOutput
	err error

	gotInput *secretsmanager.GetSecretValueInput
}

func (f *fakeSecretsManager) GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	f.gotInput = in
	return f.out, f.err
}

type fakeSSM struct {
	getOut *ssm.GetParameterOutput
	getErr error

	gotGetInput *ssm.GetParameterInput

	// byPathPages is consumed in order, one per call to GetParametersByPath.
	byPathPages []*ssm.GetParametersByPathOutput
	byPathErr   error
	byPathCalls []*ssm.GetParametersByPathInput
}

func (f *fakeSSM) GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.gotGetInput = in
	return f.getOut, f.getErr
}

func (f *fakeSSM) GetParametersByPath(ctx context.Context, in *ssm.GetParametersByPathInput, opts ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	f.byPathCalls = append(f.byPathCalls, in)
	if f.byPathErr != nil {
		return nil, f.byPathErr
	}
	idx := len(f.byPathCalls) - 1
	if idx >= len(f.byPathPages) {
		return &ssm.GetParametersByPathOutput{}, nil
	}
	return f.byPathPages[idx], nil
}

func TestSourceSecretPrefersSecretString(t *testing.T) {
	sm := &fakeSecretsManager{out: &secretsmanager.GetSecretValueOutput{
		SecretString: awssdk.String("hunter2"),
		SecretBinary: []byte("should-not-be-used"),
	}}
	src := &Source{sm: sm, ssm: &fakeSSM{}}

	val, err := src.Secret(context.Background(), "orders/db")
	if err != nil {
		t.Fatalf("Secret() error = %v", err)
	}
	if val != "hunter2" {
		t.Errorf("Secret() = %q, want hunter2", val)
	}
	if awssdk.ToString(sm.gotInput.SecretId) != "orders/db" {
		t.Errorf("SecretId = %v", sm.gotInput.SecretId)
	}
}

func TestSourceSecretFallsBackToBinary(t *testing.T) {
	sm := &fakeSecretsManager{out: &secretsmanager.GetSecretValueOutput{
		SecretBinary: []byte("binary-secret"),
	}}
	src := &Source{sm: sm, ssm: &fakeSSM{}}

	val, err := src.Secret(context.Background(), "orders/db")
	if err != nil {
		t.Fatalf("Secret() error = %v", err)
	}
	if val != "binary-secret" {
		t.Errorf("Secret() = %q, want binary-secret", val)
	}
}

func TestSourceSecretNotFound(t *testing.T) {
	sm := &fakeSecretsManager{err: &smtypes.ResourceNotFoundException{Message: awssdk.String("nope")}}
	src := &Source{sm: sm, ssm: &fakeSSM{}}

	_, err := src.Secret(context.Background(), "orders/missing")
	if !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Secret() error = %v, want secrets.ErrNotFound", err)
	}
}

func TestSourceSecretOtherErrorPassesThrough(t *testing.T) {
	boom := errors.New("throttled")
	sm := &fakeSecretsManager{err: boom}
	src := &Source{sm: sm, ssm: &fakeSSM{}}

	_, err := src.Secret(context.Background(), "orders/db")
	if !errors.Is(err, boom) {
		t.Fatalf("Secret() error = %v, want %v", err, boom)
	}
	if errors.Is(err, secrets.ErrNotFound) {
		t.Error("Secret() error should not be ErrNotFound")
	}
}

func TestSourceParameterSetsDecryption(t *testing.T) {
	ssmFake := &fakeSSM{getOut: &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{Value: awssdk.String("hunter2")},
	}}
	src := &Source{sm: &fakeSecretsManager{}, ssm: ssmFake}

	val, err := src.Parameter(context.Background(), "/orders/db-host")
	if err != nil {
		t.Fatalf("Parameter() error = %v", err)
	}
	if val != "hunter2" {
		t.Errorf("Parameter() = %q, want hunter2", val)
	}
	if ssmFake.gotGetInput.WithDecryption == nil || !*ssmFake.gotGetInput.WithDecryption {
		t.Error("WithDecryption not set to true")
	}
	if awssdk.ToString(ssmFake.gotGetInput.Name) != "/orders/db-host" {
		t.Errorf("Name = %v", ssmFake.gotGetInput.Name)
	}
}

func TestSourceParameterNotFound(t *testing.T) {
	ssmFake := &fakeSSM{getErr: &ssmtypes.ParameterNotFound{}}
	src := &Source{sm: &fakeSecretsManager{}, ssm: ssmFake}

	_, err := src.Parameter(context.Background(), "/orders/missing")
	if !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Parameter() error = %v, want secrets.ErrNotFound", err)
	}
}

func TestSourceParametersByPathPaginates(t *testing.T) {
	ssmFake := &fakeSSM{
		byPathPages: []*ssm.GetParametersByPathOutput{
			{
				Parameters: []ssmtypes.Parameter{
					{Name: awssdk.String("/orders/a"), Value: awssdk.String("va")},
				},
				NextToken: awssdk.String("page2"),
			},
			{
				Parameters: []ssmtypes.Parameter{
					{Name: awssdk.String("/orders/b"), Value: awssdk.String("vb")},
				},
				// no NextToken: last page
			},
		},
	}
	src := &Source{sm: &fakeSecretsManager{}, ssm: ssmFake}

	got, err := src.ParametersByPath(context.Background(), "/orders")
	if err != nil {
		t.Fatalf("ParametersByPath() error = %v", err)
	}
	want := map[string]string{"/orders/a": "va", "/orders/b": "vb"}
	if len(got) != len(want) {
		t.Fatalf("ParametersByPath() = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("ParametersByPath()[%q] = %q, want %q", k, got[k], v)
		}
	}
	if len(ssmFake.byPathCalls) != 2 {
		t.Fatalf("GetParametersByPath called %d times, want 2", len(ssmFake.byPathCalls))
	}
	if ssmFake.byPathCalls[1].NextToken == nil || *ssmFake.byPathCalls[1].NextToken != "page2" {
		t.Errorf("second call NextToken = %v, want page2", ssmFake.byPathCalls[1].NextToken)
	}
	for i, call := range ssmFake.byPathCalls {
		if call.Recursive == nil || !*call.Recursive {
			t.Errorf("call %d: Recursive not set to true", i)
		}
		if call.WithDecryption == nil || !*call.WithDecryption {
			t.Errorf("call %d: WithDecryption not set to true", i)
		}
	}
}

func TestSourceParametersByPathOtherErrorPassesThrough(t *testing.T) {
	boom := errors.New("throttled")
	ssmFake := &fakeSSM{byPathErr: boom}
	src := &Source{sm: &fakeSecretsManager{}, ssm: ssmFake}

	_, err := src.ParametersByPath(context.Background(), "/orders")
	if !errors.Is(err, boom) {
		t.Fatalf("ParametersByPath() error = %v, want %v", err, boom)
	}
}
