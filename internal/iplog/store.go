package iplog

import "github.com/NodePath81/fbforward/internal/audit"

// Store is retained as a compatibility name. The audit package owns the
// schema, migrations and SQL implementation.
type Store = audit.Store

func NewStore(dbPath string) (*Store, error) {
	return audit.NewStore(dbPath)
}

func NormalizeQueryParams(params QueryParams) (QueryParams, error) {
	return audit.NormalizeQueryParams(params)
}

func NormalizeRejectionQueryParams(params RejectionQueryParams) (RejectionQueryParams, error) {
	return audit.NormalizeRejectionQueryParams(params)
}

func NormalizeLogEventQueryParams(params LogEventQueryParams) (LogEventQueryParams, error) {
	return audit.NormalizeLogEventQueryParams(params)
}
