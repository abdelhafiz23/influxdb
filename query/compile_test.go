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
		{
			name: "CountWrongNumberOfArguments_TooFew",
			stmt: `SELECT count() FROM cpu`,
			err:  `invalid number of arguments for count, expected 1, got 0`,
		},
		{
			name: "CountWrongNumberOfArguments_TooMany",
			stmt: `SELECT count(value, host) FROM cpu`,
			err:  `invalid number of arguments for count, expected 1, got 2`,
		},
		{
			name: "MinWrongNumberOfArguments_TooFew",
			stmt: `SELECT min() FROM cpu`,
			err:  `invalid number of arguments for min, expected 1, got 0`,
		},
		{
			name: "MinWrongNumberOfArguments_TooMany",
			stmt: `SELECT min(value, host) FROM cpu`,
			err:  `invalid number of arguments for min, expected 1, got 2`,
		},
		{
			name: "MaxWrongNumberOfArguments_TooFew",
			stmt: `SELECT max() FROM cpu`,
			err:  `invalid number of arguments for max, expected 1, got 0`,
		},
		{
			name: "MaxWrongNumberOfArguments_TooMany",
			stmt: `SELECT max(value, host) FROM cpu`,
			err:  `invalid number of arguments for max, expected 1, got 2`,
		},
		{
			name: "SumWrongNumberOfArguments_TooFew",
			stmt: `SELECT sum() FROM cpu`,
			err:  `invalid number of arguments for sum, expected 1, got 0`,
		},
		{
			name: "SumWrongNumberOfArguments_TooMany",
			stmt: `SELECT sum(value, host) FROM cpu`,
			err:  `invalid number of arguments for sum, expected 1, got 2`,
		},
		{
			name: "FirstWrongNumberOfArguments_TooFew",
			stmt: `SELECT first() FROM cpu`,
			err:  `invalid number of arguments for first, expected 1, got 0`,
		},
		{
			name: "FirstWrongNumberOfArguments_TooMany",
			stmt: `SELECT first(value, host) FROM cpu`,
			err:  `invalid number of arguments for first, expected 1, got 2`,
		},
		{
			name: "LastWrongNumberOfArguments_TooFew",
			stmt: `SELECT last() FROM cpu`,
			err:  `invalid number of arguments for last, expected 1, got 0`,
		},
		{
			name: "LastWrongNumberOfArguments_TooMany",
			stmt: `SELECT last(value, host) FROM cpu`,
			err:  `invalid number of arguments for last, expected 1, got 2`,
		},
		{
			name: "MeanWrongNumberOfArguments_TooFew",
			stmt: `SELECT mean() FROM cpu`,
			err:  `invalid number of arguments for mean, expected 1, got 0`,
		},
		{
			name: "MeanWrongNumberOfArguments_TooMany",
			stmt: `SELECT mean(value, host) FROM cpu`,
			err:  `invalid number of arguments for mean, expected 1, got 2`,
		},
		{
			name: "DistinctWithOtherFunction",
			stmt: `SELECT distinct(value), max(value) FROM cpu`,
			err:  `aggregate function distinct() cannot be combined with other functions or fields`,
		},
		{
			name: "CountDistinctWithOtherFunction",
			stmt: `SELECT count(distinct(value)), max(value) FROM cpu`,
			err:  `aggregate function distinct() cannot be combined with other functions or fields`,
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
