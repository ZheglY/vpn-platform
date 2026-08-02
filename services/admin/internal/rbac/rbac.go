package rbac

import "slices"

var rolePermissions = map[string][]string{
	"support_readonly": {
		"identity.read", "consent.read", "subscription.read", "access.read", "provisioning.read",
		"notification.read", "notification.dlq.read", "health.read", "audit.read",
	},
	"operations": {
		"identity.read", "consent.read", "subscription.read", "access.read", "provisioning.read",
		"notification.read", "notification.dlq.read", "health.read", "audit.read",
		"notification.retry", "subscription.revoke", "access.provisioning.recover",
	},
	"security":         {"identity.read", "notification.dlq.read", "health.read", "audit.read"},
	"finance_readonly": {"billing.read", "health.read"},
}

func Permissions(roles []string) ([]string, bool) {
	seen := make(map[string]struct{})
	for _, role := range roles {
		permissions, ok := rolePermissions[role]
		if !ok {
			return nil, false
		}
		for _, permission := range permissions {
			seen[permission] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for permission := range seen {
		result = append(result, permission)
	}
	slices.Sort(result)
	return result, len(result) > 0
}

func Allowed(permissions []string, required string) bool {
	return slices.Contains(permissions, required)
}
