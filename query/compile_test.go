package query_test

import (
	"testing"
	"time"

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
		{
			name: "CountDistinctNoArguments",
			stmt: `SELECT count(distinct()) FROM cpu`,
			err:  `distinct function requires at least one argument`,
		},
		{
			name: "CountDistinctTooManyArguments",
			stmt: `SELECT count(distinct(value, host)) FROM cpu`,
			err:  `distinct function can only have one argument`,
		},
		{
			name: "CountDistinctInvalidArgument",
			stmt: `SELECT count(distinct(2)) FROM cpu`,
			err:  `expected field argument in distinct()`,
		},
		{
			name: "InvalidDimensionFunction",
			stmt: `SELECT value FROM cpu GROUP BY now()`,
			err:  `only time() calls allowed in dimensions`,
		},
		{
			name: "TimeDimensionsNoArgument",
			stmt: `SELECT value FROM cpu GROUP BY time()`,
			err:  `time dimension expected 1 or 2 arguments`,
		},
		{
			name: "TimeDimensionsTooManyArguments",
			stmt: `SELECT value FROM cpu GROUP BY time(5m, 30s, 1ms)`,
			err:  `time dimension expected 1 or 2 arguments`,
		},
		{
			name: "TimeDimensionsInvalidArgument",
			stmt: `SELECT value FROM cpu GROUP BY time('unexpected')`,
			err:  `time dimension must have duration argument`,
		},
		{
			name: "MultipleTimeDimensions",
			stmt: `SELECT value FROM cpu GROUP BY time(5m), time(1m)`,
			err:  `multiple time dimensions not allowed`,
		},
		{
			name: "TimeDimensionsInvalidOffsetFunction",
			stmt: `SELECT value FROM cpu GROUP BY time(5m, unexpected())`,
			err:  `time dimension offset function must be now()`,
		},
		{
			name: "TimeDimensionsOffsetTooManyArguments",
			stmt: `SELECT value FROM cpu GROUP BY time(5m, now(1m))`,
			err:  `time dimension offset now() function requires no arguments`,
		},
		{
			name: "TimeDimensionsInvalidOffsetArgument",
			stmt: `SELECT value FROM cpu GROUP BY time(5m, 'unexpected')`,
			err:  `time dimension offset must be duration or now()`,
		},
		{
			name: "InvalidDimension",
			stmt: `SELECT value FROM cpu GROUP BY 'unexpected'`,
			err:  `only time and tag dimensions allowed`,
		},
		{
			name: "TopFunctionNoArguments",
			stmt: `SELECT top(value) FROM cpu`,
			err:  `invalid number of arguments for top, expected at least 2, got 1`,
		},
		{
			name: "TopFunctionInvalidFieldArgument",
			stmt: `SELECT top('unexpected', 5) FROM cpu`,
			err:  `expected field argument in top()`,
		},
		{
			name: "TopFunctionInvalidDimensions",
			stmt: `SELECT top(value, 'unexpected', 5) FROM cpu`,
			err:  `only fields or tags are allowed in top(), found 'unexpected'`,
		},
		{
			name: "TopFunctionInvalidLimit",
			stmt: `SELECT top(value, 2.5) FROM cpu`,
			err:  `expected integer as last argument in top(), found 2.500`,
		},
		{
			name: "TopFunctionNegativeLimit",
			stmt: `SELECT top(value, -1) FROM cpu`,
			err:  `limit (-1) in top function must be at least 1`,
		},
		{
			name: "BottomFunctionNoArguments",
			stmt: `SELECT bottom(value) FROM cpu`,
			err:  `invalid number of arguments for bottom, expected at least 2, got 1`,
		},
		{
			name: "BottomFunctionInvalidFieldArgument",
			stmt: `SELECT bottom('unexpected', 5) FROM cpu`,
			err:  `expected field argument in bottom()`,
		},
		{
			name: "BottomFunctionInvalidDimensions",
			stmt: `SELECT bottom(value, 'unexpected', 5) FROM cpu`,
			err:  `only fields or tags are allowed in bottom(), found 'unexpected'`,
		},
		{
			name: "BottomFunctionInvalidLimit",
			stmt: `SELECT bottom(value, 2.5) FROM cpu`,
			err:  `expected integer as last argument in bottom(), found 2.500`,
		},
		{
			name: "BottomFunctionNegativeLimit",
			stmt: `SELECT bottom(value, -1) FROM cpu`,
			err:  `limit (-1) in bottom function must be at least 1`,
		},
		{
			name: "TimeConditionWithOr",
			stmt: `SELECT value FROM cpu WHERE time >= now() - 10m OR time < now() - 5m`,
			err:  `cannot use OR with time conditions`,
		},
		{
			name: "InvalidConditionStatement",
			stmt: `SELECT value FROM cpu WHERE value`,
			err:  `invalid condition expression: value`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := influxql.ParseStatement(tt.stmt)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			s := stmt.(*influxql.SelectStatement)

			opt := query.CompileOptions{}
			if _, err := query.Compile(s, opt); err == nil {
				t.Error("expected error")
			} else if have, want := err.Error(), tt.err; have != want {
				t.Errorf("unexpected error: %s != %s", have, want)
			}
		})
	}
}

func TestCompile_ParseCondition(t *testing.T) {
	mustParseTime := func(value string) time.Time {
		ts, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatalf("unable to parse time: %s", err)
		}
		return ts
	}

	// Set the now() time to the beginning of 2017.
	now := mustParseTime("2017-01-01T00:00:00Z")
	for _, tt := range []struct {
		cond     string
		min, max time.Time
		exp      string
		err      string
	}{
		{
			cond: `time >= now()`,
			min:  now,
		},
		{
			cond: `time < now()`,
			max:  now.Add(-time.Nanosecond),
		},
		{
			cond: `time >= now() OR host = 'server01'`,
			err:  "cannot use OR with time conditions",
		},
		{
			cond: `time >= now() AND host = 'server01'`,
			exp:  `host = 'server01'`,
			min:  now,
		},
		{
			cond: `value`,
			err:  "invalid condition expression: value",
		},
		{
			cond: `4`,
			err:  "invalid condition expression: 4",
		},
		{
			cond: `time >= 'today'`,
			err:  "invalid operation: time and *influxql.StringLiteral are not compatible",
		},
		{
			cond: `time != now()`,
			err:  "invalid time comparison operator: !=",
		},
		{
			cond: `now() <= time`,
			min:  now,
		},
		{
			cond: `time >= now() AND (host = 'server01' OR host = 'server02')`,
			min:  now,
			exp:  `host = 'server01' OR host = 'server02'`,
		},
		{
			cond: `host = 'server01' OR (time >= now() AND host = 'server02')`,
			err:  "cannot use OR with time conditions",
		},
		{
			cond: `host = 'server01' AND (time >= now() AND host = 'server02')`,
			min:  now,
			exp:  `host = 'server01' AND host = 'server02'`,
		},
	} {
		t.Run(tt.cond, func(t *testing.T) {
			valuer := influxql.NowValuer{Now: now}
			expr, err := influxql.ParseExpr(tt.cond)
			if err != nil {
				t.Fatalf("unable to parse expression: %s", err)
			}

			cond, tr, err := query.ParseCondition(expr, &valuer)
			if err != nil {
				if tt.err == "" {
					t.Fatalf("unexpected error: %s", err)
				} else if have, want := err.Error(), tt.err; have != want {
					t.Fatalf("unexpected error: %s != %s", have, want)
				}
				return
			}

			if cond != nil {
				if have, want := cond.String(), tt.exp; have != want {
					t.Errorf("unexpected condition: %s != %s", have, want)
				}
			} else if have, want := "", tt.exp; have != want {
				t.Errorf("unexpected condition: %s != %s", have, want)
			}

			if tr != nil {
				if !tr.Min.Equal(tt.min) {
					t.Errorf("unexpected min time: %s != %s", tr.Min, tt.min)
				}
				if !tr.Max.Equal(tt.max) {
					t.Errorf("unexpected max time: %s != %s", tr.Max, tt.max)
				}
			} else {
				if !tt.min.IsZero() {
					t.Errorf("unexpected min time: %s != %s", time.Time{}, tt.min)
				}
				if !tt.max.IsZero() {
					t.Errorf("unexpected max time: %s != %s", time.Time{}, tt.max)
				}
			}
		})
	}
}
