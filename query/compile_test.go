package query_test

import (
	"testing"

	"github.com/influxdata/influxdb/influxql"
	"github.com/influxdata/influxdb/query"
)

func TestCompile_Failures(t *testing.T) {
	for _, tt := range []struct {
		name string
		stmt string
		err  string
	}{
		{
			name: "TimeOnly",
			stmt: `SELECT time FROM cpu`,
			err:  `at least 1 non-time field must be queried`,
		},
		{
			name: "MixAggregateAndNonAggregate",
			stmt: `SELECT value, mean(value) FROM cpu`,
			err:  `mixing aggregate and non-aggregate queries is not supported`,
		},
		{
			name: "MultipleSelectors",
			stmt: `SELECT value, max(value), min(value) FROM cpu`,
			err:  `mixing multiple selector functions with tags or fields is not supported`,
		},
		{
			name: "TopWithOtherAggregate",
			stmt: `SELECT top(value, 10), max(value) FROM cpu`,
			err:  `selector function top() cannot be combined with other functions`,
		},
		{
			name: "BottomWithOtherAggregate",
			stmt: `SELECT bottom(value, 10), max(value) FROM cpu`,
			err:  `selector function bottom() cannot be combined with other functions`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := influxql.ParseStatement(tt.stmt)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			s := stmt.(*influxql.SelectStatement)

			if _, err := query.Compile(s); err == nil {
				t.Error("expected error")
			} else if have, want := err.Error(), tt.err; have != want {
				t.Errorf("unexpected error: %s != %s", have, want)
			}
		})
	}
}
