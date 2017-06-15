package query_test

import (
	"testing"

	"github.com/influxdata/influxdb/influxql"
	"github.com/influxdata/influxdb/query"
)

func TestNode(t *testing.T) {
	shards := make([]*query.Node, 0, 3)
	for i := 0; i < 3; i++ {
		sh := &query.IteratorCreator{
			Measurement: &influxql.Measurement{
				Name: "cpu",
			},
		}
		sh.OutputNode.Input = sh
		shards = append(shards, &sh.OutputNode)
	}
	merge := &query.Merge{InputNodes: shards}
	for _, sh := range shards {
		sh.Output = merge
	}
	merge.OutputNode.Input = merge

	plan := query.NewPlan()
	plan.AddTarget(&merge.OutputNode)

	for {
		edge := plan.FindWork()
		if edge == nil {
			return
		}
		edge.Execute()
		plan.EdgeFinished(edge)
	}
}
