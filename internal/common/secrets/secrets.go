// Package secrets stores BMC (and similar) credentials behind opaque refs.
// Passwords never appear on NormalizedAsset or in logs.
package secrets

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotFound is returned when a credential_ref cannot be resolved.
var ErrNotFound = errors.New("secrets: credential not found")

// Credential is a username/password pair held only in the secret backend.
type Credential struct {
	Username string
	Password string
}

// Backend stores and resolves credentials by opaque reference.
type Backend interface {
	Put(ctx context.Context, ref string, cred Credential) error
	Get(ctx context.Context, ref string) (Credential, error)
	Delete(ctx context.Context, ref string) error
}

// ValidateRef rejects empty or path-traversal-like refs.
func ValidateRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("secrets: empty credential_ref")
	}
	for _, c := range ref {
		if c == '/' || c == '\\' || c == 0 {
			return fmt.Errorf("secrets: invalid credential_ref")
		}
	}
	if ref == "." || ref == ".." {
		return fmt.Errorf("secrets: invalid credential_ref")
	}
	return nil
}
