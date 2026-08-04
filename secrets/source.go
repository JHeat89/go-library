package secrets

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Source implementations when a secret or
// parameter does not exist. Implementations must translate their backing
// store's not-found signal into this error so Loader can distinguish a
// missing entry from a real failure.
var ErrNotFound = errors.New("secrets: not found")

// Source fetches raw secret and parameter values from a backing store. The
// secrets/aws subpackage adapts AWS Secrets Manager and SSM Parameter Store;
// any store can implement this interface directly.
type Source interface {
	// Secret returns the raw value of the named secret.
	Secret(ctx context.Context, name string) (string, error)
	// Parameter returns the decrypted value of the named parameter.
	Parameter(ctx context.Context, name string) (string, error)
	// ParametersByPath returns every parameter under path, recursively and
	// decrypted, keyed by full parameter name.
	ParametersByPath(ctx context.Context, path string) (map[string]string, error)
}
