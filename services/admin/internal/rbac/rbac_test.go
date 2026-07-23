package rbac

import "testing"

func TestRolePermissionMatrix(t *testing.T) {
	tests := []struct {
		role       string
		permission string
		allowed    bool
	}{
		{"support_readonly", "notification.read", true},
		{"support_readonly", "notification.retry", false},
		{"operations", "notification.retry", true},
		{"operations", "subscription.revoke", true},
		{"security", "audit.read", true},
		{"security", "access.provisioning.recover", false},
		{"finance_readonly", "billing.read", true},
		{"finance_readonly", "subscription.read", false},
	}
	for _, test := range tests {
		permissions, ok := Permissions([]string{test.role})
		if !ok || Allowed(permissions, test.permission) != test.allowed {
			t.Fatalf("role=%s permission=%s allowed=%v permissions=%v", test.role, test.permission, test.allowed, permissions)
		}
	}
}

func TestUnknownRoleAndPermissionDefaultDeny(t *testing.T) {
	if _, ok := Permissions([]string{"superadmin"}); ok {
		t.Fatal("unknown broad role was accepted")
	}
	permissions, ok := Permissions([]string{"operations"})
	if !ok || Allowed(permissions, "*") || Allowed(permissions, "payment.mark_succeeded") {
		t.Fatal("default deny failed")
	}
}
