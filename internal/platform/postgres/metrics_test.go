package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

func TestQueryOperationIsBounded(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		sql  string
		want string
	}{
		{" SELECT secret FROM payments", "select"},
		{"INSERT INTO outbox VALUES ($1)", "insert"},
		{"UPDATE subscriptions SET status=$1", "update"},
		{"DELETE FROM inbox", "delete"},
		{"WITH due AS (SELECT 1) UPDATE jobs SET state='processing'", "with"},
		{"BEGIN", "transaction"},
		{"CREATE TABLE private_data()", "other"},
		{"", "other"},
	} {
		if got := queryOperation(test.sql); got != test.want {
			t.Fatalf("queryOperation(%q) = %q, want %q", test.sql, got, test.want)
		}
	}
}

func TestQueryMetricsDoNotExposeSQL(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	metrics := newQueryMetrics(registry, "billing-service")
	ctx := metrics.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{
		SQL:  "SELECT * FROM payments WHERE provider_payment_id='provider-secret'",
		Args: []any{"user-secret"},
	})
	metrics.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("database failure")})

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	encoder := expfmt.NewEncoder(&output, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, family := range families {
		if err := encoder.Encode(family); err != nil {
			t.Fatal(err)
		}
	}
	serialized := output.String()
	if !strings.Contains(serialized, `operation="select"`) || !strings.Contains(serialized, `outcome="error"`) {
		t.Fatalf("bounded query labels are missing:\n%s", serialized)
	}
	for _, forbidden := range []string{"provider-secret", "user-secret", "payments", "SELECT"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("query metric leaked %q:\n%s", forbidden, serialized)
		}
	}
}
