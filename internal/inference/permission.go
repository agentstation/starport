package inference

import "context"

// Permission rechecks current authority without storage reads or permission renewal.
type Permission interface{ Check() error }
type permissionKey struct{}

// WithPermission binds the original request permission to retries and cached delivery.
func WithPermission(ctx context.Context, permission Permission) context.Context {
	return context.WithValue(ctx, permissionKey{}, permission)
}

// RequestPermission returns the permission inherited by this execution context.
func RequestPermission(ctx context.Context) Permission {
	permission, _ := ctx.Value(permissionKey{}).(Permission)
	return permission
}

// CheckPermission checks a bound permission. Internal calls can carry no caller policy.
func CheckPermission(ctx context.Context) error {
	if permission := RequestPermission(ctx); permission != nil {
		return permission.Check()
	}
	return nil
}
